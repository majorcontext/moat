package routing

import (
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// endpointLink is a single named endpoint and its externally-reachable URL.
type endpointLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// agentEntry groups an agent with its base URL and endpoint links.
type agentEntry struct {
	Name      string         `json:"name"`
	BaseURL   string         `json:"base_url"`
	Endpoints []endpointLink `json:"endpoints"`
}

// wantsJSON reports whether the client prefers a JSON response over HTML.
// Browsers always send text/html in their Accept header, so they fall through
// to the HTML page; scripted clients (curl/jq) asking for application/json get
// JSON.
func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

// requestScheme returns the scheme the index links should use, mirroring the
// scheme the request arrived on so links stay on the same protocol.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// hostURL builds a URL for a hostname under the proxy, reusing the request's
// scheme and port so the link points back at this same proxy.
func hostURL(scheme, sub, port string) string {
	host := sub + ".localhost"
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}

// buildAgentEntry collects the sorted endpoint links for a single agent.
func buildAgentEntry(scheme, agent, port string, endpoints map[string]string) agentEntry {
	links := make([]endpointLink, 0, len(endpoints))
	for name := range endpoints {
		links = append(links, endpointLink{
			Name: name,
			URL:  hostURL(scheme, name+"."+agent, port),
		})
	}
	sort.Slice(links, func(i, j int) bool { return links[i].Name < links[j].Name })
	return agentEntry{
		Name:      agent,
		BaseURL:   hostURL(scheme, agent, port),
		Endpoints: links,
	}
}

// writeGlobalIndex serves a discovery page listing every running agent and its
// endpoints. Served at the bare proxy root (e.g. http://localhost:8080).
func (rp *ReverseProxy) writeGlobalIndex(w http.ResponseWriter, r *http.Request, port string) {
	scheme := requestScheme(r)
	routes := rp.routes.Snapshot()

	agents := make([]agentEntry, 0, len(routes))
	for agent, endpoints := range routes {
		agents = append(agents, buildAgentEntry(scheme, agent, port, endpoints))
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
		return
	}
	renderHTML(w, indexPage{Title: "Moat — running agents", Agents: agents})
}

// writeAgentIndex serves a discovery page for a single agent's endpoints.
// Served at the bare agent host (e.g. http://demo.localhost:8080) when the
// agent exposes more than one endpoint.
func (rp *ReverseProxy) writeAgentIndex(w http.ResponseWriter, r *http.Request, agent string, endpoints map[string]string, port string) {
	scheme := requestScheme(r)
	entry := buildAgentEntry(scheme, agent, port, endpoints)

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, entry)
		return
	}
	renderHTML(w, indexPage{Title: agent + " — endpoints", Agents: []agentEntry{entry}})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// The status line is already committed, so an encode error can't change the
	// response — but log it so encoding bugs aren't invisible.
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Debug("index json encode error", "error", err)
	}
}

// indexPage is the view model for the HTML discovery page.
type indexPage struct {
	Title  string
	Agents []agentEntry
}

func renderHTML(w http.ResponseWriter, page indexPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// As with writeJSON, the status is committed; log execution errors so
	// template bugs are visible.
	if err := indexTemplate.Execute(w, page); err != nil {
		log.Debug("index template execute error", "error", err)
	}
}

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
         max-width: 720px; margin: 3rem auto; padding: 0 1.25rem; }
  h1 { font-size: 1.4rem; margin: 0 0 1.5rem; }
  h2 { font-size: 1.05rem; margin: 0; }
  .agent { border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
           border-radius: 10px; padding: 1rem 1.25rem; margin-bottom: 1rem; }
  .agent a.base { font-size: 0.85rem; opacity: 0.7; text-decoration: none; }
  ul { list-style: none; margin: 0.75rem 0 0; padding: 0; }
  li { display: flex; align-items: baseline; gap: 0.75rem; padding: 0.3rem 0; }
  .name { font-weight: 600; min-width: 5rem; }
  a.endpoint { text-decoration: none; }
  a.endpoint:hover { text-decoration: underline; }
  .empty { opacity: 0.7; }
  footer { margin-top: 2rem; font-size: 0.8rem; opacity: 0.55; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
{{if .Agents}}
{{range .Agents}}
<section class="agent">
  <h2>{{.Name}}</h2>
  <a class="base" href="{{.BaseURL}}">{{.BaseURL}}</a>
  {{if .Endpoints}}
  <ul>
    {{range .Endpoints}}
    <li><span class="name">{{.Name}}</span><a class="endpoint" href="{{.URL}}">{{.URL}}</a></li>
    {{end}}
  </ul>
  {{end}}
</section>
{{end}}
{{else}}
<p class="empty">No agents are currently running.</p>
{{end}}
<footer>moat routing proxy</footer>
</body>
</html>
`))
