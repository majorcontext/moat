package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/andybons/agentops/internal/config"
	"github.com/andybons/agentops/internal/log"
	"github.com/andybons/agentops/internal/routing"
	"github.com/spf13/cobra"
)

var proxyPort int

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Manage the routing proxy",
	Long: `Manage the hostname-based routing proxy.

The routing proxy enables accessing agent services via hostnames like:
  http://web.my-agent.localhost:8080

Run with sudo to bind to privileged ports like 80:
  sudo agent proxy start --port=80`,
}

var proxyStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the routing proxy",
	Long: `Start the routing proxy in the foreground.

The proxy routes requests based on hostname:
  http://<service>.<agent>.localhost:<port> -> container service

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
		routing.RemoveProxyLock(proxyDir)
	}

	// Create route table
	routes, err := routing.NewRouteTable(proxyDir)
	if err != nil {
		return fmt.Errorf("creating route table: %w", err)
	}

	// Start proxy
	server := routing.NewProxyServer(routes)
	if err := server.Start(proxyPort); err != nil {
		return fmt.Errorf("starting proxy: %w", err)
	}

	// Save lock file
	if err := routing.SaveProxyLock(proxyDir, routing.ProxyLockInfo{
		PID:  os.Getpid(),
		Port: server.Port(),
	}); err != nil {
		server.Stop(context.Background())
		return fmt.Errorf("saving proxy lock: %w", err)
	}

	log.Info("proxy started", "port", server.Port(), "pid", os.Getpid())
	fmt.Printf("Routing proxy listening on port %d\n", server.Port())
	fmt.Printf("Access services at: http://<service>.<agent>.localhost:%d\n", server.Port())

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down proxy...")
	if err := server.Stop(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", err)
	}
	routing.RemoveProxyLock(proxyDir)

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
		routing.RemoveProxyLock(proxyDir)
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

	// Show registered routes
	routes, err := routing.NewRouteTable(proxyDir)
	if err == nil {
		agents := routes.Agents()
		if len(agents) > 0 {
			fmt.Println("\nRegistered agents:")
			for _, agent := range agents {
				fmt.Printf("  - %s.localhost:%d\n", agent, lock.Port)
			}
		}
	}

	return nil
}
