package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var proxyPort int

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Manage the routing proxy",
	Long: `Manage the hostname-based routing proxy.

The routing proxy enables accessing agent services via hostnames like:
  https://web.my-agent.localhost:8080

Run with sudo to bind to privileged ports like 80:
  sudo agent proxy start --port=80

When called without a subcommand, shows the current proxy status.`,
	RunE: statusProxy,
}

var proxyStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the routing proxy",
	Long: `Start the routing proxy in the foreground.

The proxy routes requests based on hostname and supports both HTTP and HTTPS:
  http://<service>.<agent>.localhost:<port> -> container service
  https://<service>.<agent>.localhost:<port> -> container service (TLS)

Use --port to specify a custom port (default: 8080).
Run with sudo for ports below 1024:
  sudo agent proxy start --port=80`,
	RunE: startProxy,
}

var proxyStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the routing proxy",
	Long:  `Stop the running routing proxy.`,
	RunE:  stopProxy,
}

var proxyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show proxy status",
	Long:  `Show whether the routing proxy is running and on which port.`,
	RunE:  statusProxy,
}

func init() {
	proxyStartCmd.Flags().IntVarP(&proxyPort, "port", "p", 8080, "port to listen on")

	proxyCmd.AddCommand(proxyStartCmd)
	proxyCmd.AddCommand(proxyStopCmd)
	proxyCmd.AddCommand(proxyStatusCmd)
	rootCmd.AddCommand(proxyCmd)
}

func startProxy(cmd *cobra.Command, args []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")

	// Check if already running
	lock, err := routing.LoadProxyLock(proxyDir)
	if err != nil {
		return fmt.Errorf("checking proxy status: %w", err)
	}
	if lock != nil && lock.IsAlive() {
		return fmt.Errorf("proxy already running on port %d (pid %d)", lock.Port, lock.PID)
	}

	// Clean up stale lock
	if lock != nil {
		_ = routing.RemoveProxyLock(proxyDir)
	}

	// Create lifecycle manager
	lc, err := routing.NewLifecycle(proxyDir, proxyPort)
	if err != nil {
		return fmt.Errorf("creating lifecycle: %w", err)
	}

	// Enable TLS
	newCA, err := lc.EnableTLS()
	if err != nil {
		return fmt.Errorf("enabling TLS: %w", err)
	}

	// Start proxy
	if err := lc.EnsureRunning(); err != nil {
		return fmt.Errorf("starting proxy: %w", err)
	}

	log.Info("proxy started", "port", lc.Port(), "pid", os.Getpid())
	fmt.Printf("Proxy listening on port %d (HTTP and HTTPS)\n", lc.Port())
	fmt.Printf("Access services at:\n")
	fmt.Printf("  http://<service>.<agent>.localhost:%d\n", lc.Port())
	fmt.Printf("  https://<service>.<agent>.localhost:%d\n", lc.Port())

	// Print trust instructions if new CA was created
	if newCA {
		caPath := filepath.Join(proxyDir, "ca", "ca.crt")
		fmt.Printf("\nGenerated CA certificate at %s\n", caPath)
		fmt.Println("To avoid browser warnings, trust the CA:")
		switch runtime.GOOS {
		case "darwin":
			fmt.Printf("  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s\n", caPath)
		case "linux":
			fmt.Printf("  sudo cp %s /usr/local/share/ca-certificates/moat.crt && sudo update-ca-certificates\n", caPath)
		default:
			fmt.Printf("  Add %s to your system's trusted certificates\n", caPath)
		}
	}

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down proxy...")
	if err := lc.Stop(context.Background()); err != nil {
		ui.Warnf("Stopping proxy: %v", err)
	}

	return nil
}

func stopProxy(cmd *cobra.Command, args []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")

	lock, err := routing.LoadProxyLock(proxyDir)
	if err != nil {
		return fmt.Errorf("checking proxy status: %w", err)
	}

	if lock == nil {
		fmt.Println("Proxy is not running")
		return nil
	}

	if !lock.IsAlive() {
		// Clean up stale lock
		_ = routing.RemoveProxyLock(proxyDir)
		fmt.Println("Proxy is not running (cleaned up stale lock)")
		return nil
	}

	// Send SIGTERM to the proxy process
	process, err := os.FindProcess(lock.PID)
	if err != nil {
		return fmt.Errorf("finding proxy process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stopping proxy: %w", err)
	}

	fmt.Printf("Stopped proxy (pid %d)\n", lock.PID)
	return nil
}

func statusProxy(cmd *cobra.Command, args []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")

	lock, err := routing.LoadProxyLock(proxyDir)
	if err != nil {
		return fmt.Errorf("checking proxy status: %w", err)
	}

	if lock == nil {
		fmt.Println("Proxy is not running")
		return nil
	}

	if !lock.IsAlive() {
		fmt.Println("Proxy is not running (stale lock file exists)")
		return nil
	}

	fmt.Printf("Proxy running on port %d (pid %d)\n", lock.Port, lock.PID)
	fmt.Printf("Supports HTTP and HTTPS on the same port\n")

	// Show registered routes
	routes, err := routing.NewRouteTable(proxyDir)
	if err == nil {
		agents := routes.Agents()
		if len(agents) > 0 {
			fmt.Println("\nRegistered agents:")
			for _, agent := range agents {
				fmt.Printf("  - https://%s.localhost:%d\n", agent, lock.Port)
			}
		}
	}

	return nil
}
