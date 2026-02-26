package cli

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/proxy"
	"github.com/spf13/cobra"
)

var daemonDir string
var daemonProxyPort int

var daemonCmd = &cobra.Command{
	Use:    "_daemon",
	Hidden: true,
	Short:  "Run the proxy daemon (internal use)",
	RunE:   runDaemon,
}

func init() {
	daemonCmd.Flags().StringVar(&daemonDir, "dir", "", "daemon working directory")
	daemonCmd.Flags().IntVar(&daemonProxyPort, "proxy-port", 0, "proxy port")
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(_ *cobra.Command, _ []string) error {
	if daemonDir == "" {
		daemonDir = filepath.Join(config.GlobalConfigDir(), "proxy")
	}

	sockPath := filepath.Join(daemonDir, "daemon.sock")

	// Create API server.
	apiServer := daemon.NewServer(sockPath, daemonProxyPort)

	// Create credential proxy.
	p := proxy.NewProxy()

	// Set up CA for TLS interception.
	caDir := filepath.Join(daemonDir, "ca")
	ca, err := proxy.NewCA(caDir)
	if err != nil {
		return err
	}
	p.SetCA(ca)

	// Wire the proxy's context resolver to the API server's registry.
	p.SetContextResolver(func(token string) (*proxy.RunContextData, bool) {
		rc, ok := apiServer.Registry().Lookup(token)
		if !ok {
			return nil, false
		}
		return rc.ToProxyContextData(), true
	})

	// Start credential proxy.
	proxyServer := proxy.NewServer(p)
	proxyServer.SetBindAddr("0.0.0.0")
	if daemonProxyPort > 0 {
		proxyServer.SetPort(daemonProxyPort)
	}
	if err := proxyServer.Start(); err != nil {
		return err
	}

	// Determine the actual port the proxy is listening on.
	actualPort := daemonProxyPort
	if actualPort == 0 {
		actualPort, _ = strconv.Atoi(proxyServer.Port())
	}

	// Start API server.
	if err := apiServer.Start(); err != nil {
		_ = proxyServer.Stop(context.Background())
		return err
	}

	// Write lock file.
	if err := daemon.WriteLockFile(daemonDir, daemon.LockInfo{
		PID:       os.Getpid(),
		ProxyPort: actualPort,
		SockPath:  sockPath,
	}); err != nil {
		_ = apiServer.Stop(context.Background())
		_ = proxyServer.Stop(context.Background())
		return err
	}

	log.Info("daemon started", "pid", os.Getpid(), "proxy_port", actualPort, "sock", sockPath)

	// Set up idle auto-shutdown (5 min).
	idleShutdown := daemon.NewIdleTimer(5*time.Minute, func() {
		log.Info("daemon idle timeout, shutting down")
		_ = apiServer.Stop(context.Background())
		_ = proxyServer.Stop(context.Background())
		daemon.RemoveLockFile(daemonDir)
	})
	apiServer.SetOnEmpty(idleShutdown.Reset)

	// Wait for signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("daemon shutting down")
	idleShutdown.Cancel()
	_ = apiServer.Stop(context.Background())
	_ = proxyServer.Stop(context.Background())
	daemon.RemoveLockFile(daemonDir)

	return nil
}
