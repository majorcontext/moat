package githubscope_test

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/cgi" //nolint:gosec // test-only CGI host for git-http-backend; Go has fixed httpoxy since 1.6.3
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/majorcontext/gatekeeper/proxy"
	keeplib "github.com/majorcontext/keep"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/githubscope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	e2eWorkspaceNodeID = "R_kgDOWORKSPACE"
	e2eOtherNodeID     = "R_kgDOOTHER"
)

// fakeGitHub serves git smart-HTTP (via git-http-backend) for bare
// repositories under root, and a stub GraphQL endpoint.
func fakeGitHub(t *testing.T, root string) *httptest.Server {
	t.Helper()
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skip("git not available")
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git-http-backend not available")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	mux.Handle("/", &cgi.Handler{
		Path: backend,
		Root: "/",
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func runGit(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestPolicyThroughProxy runs the rendered policy in the real gatekeeper proxy
// (TLS interception, per-request body buffering, Keep evaluation) and drives
// it with git and GraphQL clients the way an agent would.
func TestPolicyThroughProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end proxy test")
	}
	root := t.TempDir()
	home := t.TempDir()
	gitEnv := []string{
		"HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "PATH=" + os.Getenv("PATH"),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	}
	for _, repo := range []string{"majorcontext/moat.git", "acme/other.git"} {
		dir := filepath.Join(root, repo)
		_, err := runGit(t, root, gitEnv, "init", "-q", "--bare", dir)
		require.NoError(t, err)
		_, err = runGit(t, dir, gitEnv, "config", "http.receivepack", "true")
		require.NoError(t, err)
	}
	backend := fakeGitHub(t, root)
	backendURL, _ := url.Parse(backend.URL)
	port, _ := strconv.Atoi(backendURL.Port())

	policy, err := githubscope.RenderPolicy(githubscope.PolicyParams{
		Repos:      []string{"majorcontext/moat"},
		NodeIDs:    []string{e2eWorkspaceNodeID},
		OtherRepos: githubscope.OtherReposRead,
	})
	require.NoError(t, err)
	eng, err := keeplib.LoadFromBytes(policy)
	require.NoError(t, err)
	t.Cleanup(eng.Close)

	caDir := t.TempDir()
	ca, err := proxy.NewCAWithSubject(caDir, daemon.CAOrganization, daemon.CACommonName)
	require.NoError(t, err)
	upstream := x509.NewCertPool()
	upstream.AddCert(backend.Certificate())

	p := proxy.NewProxy()
	p.SetBranding(daemon.Branding())
	p.SetCA(ca)
	p.SetUpstreamCAs(upstream)
	// One token per fake host: the host-gateway mechanism maps a single
	// hostname to the local backend.
	p.SetContextResolver(func(token string) (*proxy.RunContextData, bool) {
		hosts := map[string]string{"gh": "github.com", "api": "api.github.com"}
		h, ok := hosts[token]
		if !ok {
			return nil, false
		}
		return &proxy.RunContextData{
			Policy:           "permissive",
			HostGateway:      h,
			HostGatewayIP:    "127.0.0.1",
			AllowedHostPorts: []int{port},
			KeepEngines:      map[string]*keeplib.Engine{"http": eng},
		}, true
	})
	proxySrv := httptest.NewServer(p)
	t.Cleanup(proxySrv.Close)
	proxyURL, _ := url.Parse(proxySrv.URL)

	t.Run("git", func(t *testing.T) {
		gitProxy := "http://u:gh@" + proxyURL.Host
		env := append(append([]string{}, gitEnv...),
			"HTTPS_PROXY="+gitProxy, "https_proxy="+gitProxy, "NO_PROXY=", "no_proxy=",
			"GIT_SSL_CAINFO="+filepath.Join(caDir, "ca.crt"))
		work := t.TempDir()
		_, err := runGit(t, work, env, "init", "-q", "-b", "main")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(work, "f.txt"), []byte("hello\n"), 0o644))
		_, err = runGit(t, work, env, "add", "f.txt")
		require.NoError(t, err)
		_, err = runGit(t, work, env, "commit", "-q", "-m", "c")
		require.NoError(t, err)

		base := "https://github.com:" + strconv.Itoa(port)
		out, err := runGit(t, work, env, "push", base+"/majorcontext/moat.git", "main")
		require.NoError(t, err, "push to the workspace repository must succeed: %s", out)

		out, err = runGit(t, work, env, "push", base+"/acme/other.git", "main")
		require.Error(t, err, "push to another repository must fail: %s", out)
		t.Logf("blocked push, as the agent sees it:\n%s", out)
		assert.Contains(t, out, "remote: Push blocked: this run can only push to majorcontext/moat", "git shows the policy message")

		refs, _ := runGit(t, filepath.Join(root, "acme/other.git"), gitEnv, "for-each-ref")
		assert.Empty(t, strings.TrimSpace(refs), "nothing reached the other repository")

		out, err = runGit(t, work, env, "ls-remote", base+"/majorcontext/moat.git")
		require.NoError(t, err, out)
		assert.Contains(t, out, "refs/heads/main")
		_, err = runGit(t, work, env, "ls-remote", base+"/acme/other.git")
		assert.NoError(t, err, "reading another repository is allowed with other_repos: read")
	})

	t.Run("graphql", func(t *testing.T) {
		pu := *proxyURL
		pu.User = url.UserPassword("u", "api")
		cas := x509.NewCertPool()
		pem, err := os.ReadFile(filepath.Join(caDir, "ca.crt"))
		require.NoError(t, err)
		cas.AppendCertsFromPEM(pem)
		client := &http.Client{Transport: &http.Transport{
			Proxy:           http.ProxyURL(&pu),
			TLSClientConfig: &tls.Config{RootCAs: cas},
		}}
		post := func(body string) (int, http.Header) {
			resp, err := client.Post("https://api.github.com:"+strconv.Itoa(port)+"/graphql", "application/json", strings.NewReader(body))
			require.NoError(t, err)
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			return resp.StatusCode, resp.Header
		}
		createPR := `{"query":"mutation($i: CreatePullRequestInput!) { createPullRequest(input: $i) { clientMutationId } }","variables":{"i":{"repositoryId":"%s"}}}`

		status, _ := post(strings.Replace(createPR, "%s", e2eWorkspaceNodeID, 1))
		assert.Equal(t, http.StatusOK, status)
		status, header := post(strings.Replace(createPR, "%s", e2eOtherNodeID, 1))
		assert.Equal(t, http.StatusForbidden, status)
		assert.Equal(t, "keep-policy", header.Get("X-Moat-Blocked"))
		status, _ = post(`{"query":"mutation { mergePullRequest(input: {pullRequestId: \"PR_x\"}) { clientMutationId } }"}`)
		assert.Equal(t, http.StatusForbidden, status)
		status, _ = post(`{"query":"query { viewer { login } }"}`)
		assert.Equal(t, http.StatusOK, status)
	})
}
