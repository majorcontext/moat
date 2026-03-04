package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
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
  sudo moat proxy start --port=80

When called without a subcommand, shows the current proxy status.`,
	RunE: statusProxy,
}

var proxyStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the routing proxy",
	Long: `Start the routing proxy in the background.

The proxy routes requests based on hostname and supports both HTTP and HTTPS:
  http://<service>.<agent>.localhost:<port> -> container service
  https://<service>.<agent>.localhost:<port> -> container service (TLS)

Use --port to specify a custom port (default: 8080).
Run with sudo for ports below 1024:
  sudo moat proxy start --port=80`,
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

func startProxy(_ *cobra.Command, _ []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")
	client, err := daemon.EnsureRunning(proxyDir, proxyPort)
	if err != nil {
		return err
	}
	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	health, err := client.Health(healthCtx)
	if err != nil {
		return err
	}
	fmt.Printf("Proxy started on port %d\n", health.ProxyPort)
	return nil
}

func stopProxy(_ *cobra.Command, _ []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")
	sockPath := filepath.Join(proxyDir, "daemon.sock")

	client := daemon.NewClient(sockPath)
	if err := client.Shutdown(context.Background()); err != nil {
		// Try SIGTERM as fallback.
		lock, _ := daemon.ReadLockFile(proxyDir)
		if lock != nil && lock.IsAlive() {
			process, _ := os.FindProcess(lock.PID)
			_ = process.Signal(syscall.SIGTERM)
			fmt.Printf("Stopped daemon (pid %d)\n", lock.PID)
			return nil
		}
		fmt.Println("Daemon is not running")
		return nil
	}

	fmt.Println("Daemon shutdown requested")
	return nil
}

func statusProxy(_ *cobra.Command, _ []string) error {
	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")
	sockPath := filepath.Join(proxyDir, "daemon.sock")

	client := daemon.NewClient(sockPath)
	health, err := client.Health(context.Background())
	if err != nil {
		fmt.Println("Daemon is not running")
		return nil
	}

	fmt.Printf("Daemon running (pid %d)\n", health.PID)
	fmt.Printf("  Proxy port: %d\n", health.ProxyPort)
	fmt.Printf("  Active runs: %d\n", health.RunCount)
	fmt.Printf("  Started: %s\n", health.StartedAt)
	if health.Commit != "" {
		fmt.Printf("  Commit: %s (cli: %s)\n", health.Commit, commit)
	}

	// List runs.
	runs, err := client.ListRuns(context.Background())
	if err == nil && len(runs) > 0 {
		fmt.Println("\nRegistered runs:")
		for _, r := range runs {
			fmt.Printf("  - %s", r.RunID)
			if r.ContainerID != "" {
				short := r.ContainerID
				if len(short) > 12 {
					short = short[:12]
				}
				fmt.Printf(" (container: %s)", short)
			}
			fmt.Println()
		}
	}

	return nil
}
