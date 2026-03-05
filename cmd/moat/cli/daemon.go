package cli

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/proxy"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/storage"
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

	// Expose build version to daemon package so the health endpoint can
	// report it. This allows detecting version skew between daemon and CLI.
	daemon.BuildCommit = commit

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

	// Wire network request logging. The proxy is shared across runs, so
	// the logger routes to per-run storage using the RunID from request context.
	var storeMu sync.Mutex
	stores := make(map[string]*storage.RunStore)
	baseDir := storage.DefaultBaseDir()

	p.SetLogger(func(data proxy.RequestLogData) {
		if data.RunID == "" {
			return
		}

		storeMu.Lock()
		store, ok := stores[data.RunID]
		if !ok {
			var storeErr error
			store, storeErr = storage.NewRunStore(baseDir, data.RunID)
			if storeErr != nil {
				storeMu.Unlock()
				log.Warn("failed to open run store for network log",
					"run_id", data.RunID, "error", storeErr)
				return
			}
			stores[data.RunID] = store
		}
		storeMu.Unlock()

		var errStr string
		if data.Err != nil {
			errStr = data.Err.Error()
		}
		_ = store.WriteNetworkRequest(storage.NetworkRequest{
			Timestamp:       time.Now().UTC(),
			Method:          data.Method,
			URL:             data.URL,
			StatusCode:      data.StatusCode,
			Duration:        data.Duration.Milliseconds(),
			Error:           errStr,
			RequestHeaders:  proxy.FilterHeaders(data.RequestHeaders, data.AuthInjected, data.InjectedHeaderName),
			ResponseHeaders: proxy.FilterHeaders(data.ResponseHeaders, false, ""),
			RequestBody:     string(data.RequestBody),
			ResponseBody:    string(data.ResponseBody),
			BodyTruncated:   len(data.RequestBody) > proxy.MaxBodySize || len(data.ResponseBody) > proxy.MaxBodySize,
		})
	})

	// Start credential proxy.
	proxyServer := proxy.NewServer(p)
	proxyServer.SetBindAddr("0.0.0.0")
	if daemonProxyPort > 0 {
		proxyServer.SetPort(daemonProxyPort)
	}
	if startErr := proxyServer.Start(); startErr != nil {
		// If a specific port was requested (e.g. preserved from a stale daemon)
		// and it's now occupied, fall back to an OS-assigned port.
		if daemonProxyPort > 0 {
			log.Warn("proxy port unavailable, falling back to OS-assigned port",
				"requested_port", daemonProxyPort, "error", startErr)
			proxyServer.SetPort(0)
			if retryErr := proxyServer.Start(); retryErr != nil {
				return retryErr
			}
		} else {
			return startErr
		}
	}

	// Determine the actual port the proxy is listening on.
	// Always read from proxyServer.Port() since the actual port may differ
	// from daemonProxyPort after a fallback to an OS-assigned port.
	parsed, parseErr := strconv.Atoi(proxyServer.Port())
	if parseErr != nil {
		log.Warn("failed to parse proxy port", "port", proxyServer.Port(), "error", parseErr)
	}
	actualPort := parsed

	// Update API server with actual proxy port (may differ from requested if port was 0).
	apiServer.SetProxyPort(actualPort)

	// Write lock file BEFORE starting the API server. The parent's
	// EnsureRunning polls the socket for health — if the lock file isn't
	// written yet, a concurrent caller could acquire the spawn lock, see
	// no lock file, and spawn a second daemon.
	if lockErr := daemon.WriteLockFile(daemonDir, daemon.LockInfo{
		PID:       os.Getpid(),
		ProxyPort: actualPort,
		SockPath:  sockPath,
		Commit:    commit,
	}); lockErr != nil {
		_ = proxyServer.Stop(context.Background())
		return lockErr
	}

	// Start API server.
	if startErr := apiServer.Start(); startErr != nil {
		daemon.RemoveLockFile(daemonDir)
		_ = proxyServer.Stop(context.Background())
		return startErr
	}

	// Set up routing proxy.
	routeTable, err := routing.NewRouteTable(daemonDir)
	if err != nil {
		log.Warn("failed to create route table", "error", err)
	} else {
		apiServer.SetRoutes(routeTable)

		routingProxy := routing.NewProxyServer(routeTable)

		// Enable TLS for routing proxy using same CA.
		if err := routingProxy.EnableTLS(ca); err != nil {
			log.Warn("failed to enable TLS for routing proxy", "error", err)
		}

		// Start routing proxy on a random available port.
		if err := routingProxy.Start(0); err != nil {
			log.Warn("failed to start routing proxy", "error", err)
		} else {
			log.Info("routing proxy started", "port", routingProxy.Port())
			defer func() {
				_ = routingProxy.Stop(context.Background())
			}()
		}
	}

	log.Info("daemon started", "pid", os.Getpid(), "proxy_port", actualPort, "sock", sockPath)

	// Wait for signal or idle timeout.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Set up idle auto-shutdown (5 min). On timeout, send SIGTERM to self.
	idleShutdown := daemon.NewIdleTimer(5*time.Minute, func() {
		log.Info("daemon idle timeout, shutting down")
		sigCh <- syscall.SIGTERM
	})
	apiServer.SetOnRegister(idleShutdown.Cancel)
	apiServer.SetOnEmpty(idleShutdown.Reset)

	// Start container liveness checker to clean up dead runs.
	livenessCtx, livenessCancel := context.WithCancel(context.Background())
	defer livenessCancel()
	lc := daemon.NewLivenessChecker(apiServer.Registry(), daemon.NewCommandContainerChecker())
	cleanupStore := func(runID string) {
		storeMu.Lock()
		delete(stores, runID)
		storeMu.Unlock()
	}
	lc.SetOnCleanup(func(_, runID string) { cleanupStore(runID) })
	lc.SetOnEmpty(idleShutdown.Reset)

	// Set up run persistence so the registry survives daemon restarts.
	persistPath := filepath.Join(daemonDir, "runs.json")
	persister := daemon.NewRunPersister(persistPath, apiServer.Registry())
	apiServer.SetPersister(persister)
	lc.SetPersister(persister)

	// Restore previously persisted runs (re-resolves credentials from store).
	if persisted, loadErr := persister.Load(); loadErr != nil {
		log.Warn("failed to load persisted runs", "error", loadErr)
	} else if len(persisted) > 0 {
		restored := daemon.RestoreRuns(livenessCtx, apiServer.Registry(), persisted)
		log.Info("restored runs from disk", "loaded", len(persisted), "restored", restored)
		// Save immediately to reconcile (remove runs that failed to restore).
		if err := persister.Save(); err != nil {
			log.Warn("failed to save reconciled run registry", "error", err)
		}
	}

	// Run an immediate liveness check to clean up dead containers from restore,
	// then start the periodic loop. The check runs in the same goroutine to
	// avoid a 30-second window where dead runs block the idle timer, but uses
	// a tighter per-run timeout (via CheckOnce) to limit startup delay.
	go func() {
		lc.CheckOnce(livenessCtx)
		lc.Run(livenessCtx)
	}()

	// Clean up per-run stores when runs are unregistered.
	apiServer.SetOnUnregister(cleanupStore)

	// Wire API shutdown handler to signal the main loop.
	apiServer.SetOnShutdown(func() {
		sigCh <- syscall.SIGTERM
	})

	// Arm the idle timer. If runs were restored, cancel it since we have
	// active runs; otherwise arm it so the daemon shuts down if no runs
	// register within the idle timeout period.
	if apiServer.Registry().Count() > 0 {
		idleShutdown.Cancel()
	} else {
		idleShutdown.Reset()
	}
	<-sigCh

	log.Info("daemon shutting down")
	idleShutdown.Cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = apiServer.Stop(shutdownCtx)
	_ = proxyServer.Stop(shutdownCtx)

	// Stop liveness goroutine before flush so no SaveDebounced can race
	// with the final Flush() write. The defer livenessCancel() is still
	// present as a safety net — calling a CancelFunc twice is a no-op.
	livenessCancel()

	// Flush persister to ensure final state is written before removing the lock file.
	if err := persister.Flush(); err != nil {
		log.Warn("failed to flush persisted run registry", "error", err)
	}
	daemon.RemoveLockFile(daemonDir)

	return nil
}
