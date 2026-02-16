//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// TestVolumePersistenceAcrossRuns is runtime-agnostic: verifies that data written to a
// named volume in one run is available in a subsequent run with the same agent name.
func TestVolumePersistenceAcrossRuns(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		agentName := "e2e-vol-persist"

		// Clean up any leftover volumes from a previous test run.
		t.Cleanup(func() {
			cleanupVolume(t, rt, agentName, "state")
		})

		workspace := createTestWorkspaceWithVolumes(t, agentName, []config.VolumeConfig{
			{Name: "state", Target: "/data"},
		})

		// Run 1: Write a file into the volume.
		// Use a command that captures diagnostics even on failure.
		marker := "e2e-volume-marker-" + time.Now().Format("20060102150405")
		writeCmd := strings.Join([]string{
			"echo DIAG_ID=$(id)",
			"echo DIAG_MOUNT=$(mount | grep /data || echo 'no /data mount')",
			"echo DIAG_LS=$(ls -ld /data 2>&1 || echo '/data not found')",
			"echo '" + marker + "' > /data/marker.txt 2>&1 && echo WRITE_OK || echo WRITE_FAILED",
			"cat /data/marker.txt 2>&1 || true",
		}, "; ")

		r1, err := mgr.Create(ctx, run.Options{
			Name:      agentName,
			Workspace: workspace,
			Config: &config.Config{
				Name:  agentName,
				Agent: "e2e-test",
				Volumes: []config.VolumeConfig{
					{Name: "state", Target: "/data"},
				},
			},
			Cmd: []string{"sh", "-c", writeCmd},
		})
		if err != nil {
			t.Fatalf("Create run 1: %v", err)
		}

		if err := mgr.Start(ctx, r1.ID, run.StartOptions{}); err != nil {
			t.Fatalf("Start run 1: %v", err)
		}
		if err := mgr.Wait(ctx, r1.ID); err != nil {
			// Don't fatal — read logs first to get diagnostics
			t.Logf("Wait run 1 returned error: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Read logs from run 1
		store1, err := storage.NewRunStore(storage.DefaultBaseDir(), r1.ID)
		if err != nil {
			t.Fatalf("NewRunStore run 1: %v", err)
		}
		logs1, err := store1.ReadLogs(0, 100)
		if err != nil {
			t.Fatalf("ReadLogs run 1: %v", err)
		}

		// Log diagnostics
		for _, entry := range logs1 {
			if strings.HasPrefix(entry.Line, "DIAG_") {
				t.Logf("Run 1: %s", entry.Line)
			}
		}

		// Verify the write succeeded
		foundWriteOK := false
		foundMarker := false
		for _, entry := range logs1 {
			if strings.Contains(entry.Line, "WRITE_OK") {
				foundWriteOK = true
			}
			if strings.Contains(entry.Line, marker) {
				foundMarker = true
			}
		}
		if !foundWriteOK {
			t.Fatalf("Run 1: volume write failed\nLogs: %v", logs1)
		}
		if !foundMarker {
			t.Fatalf("Run 1: marker not found in output\nLogs: %v", logs1)
		}

		// Destroy run 1 (volume should persist)
		if err := mgr.Destroy(ctx, r1.ID); err != nil {
			t.Fatalf("Destroy run 1: %v", err)
		}

		// Run 2: Read the file from the volume
		r2, err := mgr.Create(ctx, run.Options{
			Name:      agentName,
			Workspace: workspace,
			Config: &config.Config{
				Name:  agentName,
				Agent: "e2e-test",
				Volumes: []config.VolumeConfig{
					{Name: "state", Target: "/data"},
				},
			},
			Cmd: []string{"sh", "-c", "cat /data/marker.txt 2>&1 || echo FILE_NOT_FOUND"},
		})
		if err != nil {
			t.Fatalf("Create run 2: %v", err)
		}
		defer mgr.Destroy(context.Background(), r2.ID)

		if err := mgr.Start(ctx, r2.ID, run.StartOptions{}); err != nil {
			t.Fatalf("Start run 2: %v", err)
		}
		if err := mgr.Wait(ctx, r2.ID); err != nil {
			t.Logf("Wait run 2 returned error: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Verify run 2 can read the marker written by run 1
		store2, err := storage.NewRunStore(storage.DefaultBaseDir(), r2.ID)
		if err != nil {
			t.Fatalf("NewRunStore run 2: %v", err)
		}
		logs2, err := store2.ReadLogs(0, 100)
		if err != nil {
			t.Fatalf("ReadLogs run 2: %v", err)
		}

		foundInRun2 := false
		for _, entry := range logs2 {
			if strings.Contains(entry.Line, marker) {
				foundInRun2 = true
				break
			}
		}
		if !foundInRun2 {
			t.Errorf("Run 2 could not read marker from volume (data did not persist)\n"+
				"Expected: %q\nLogs: %v", marker, logs2)
		}
	})
}

// TestVolumeReadOnly is runtime-agnostic: verifies that a readonly volume cannot be written to.
func TestVolumeReadOnly(t *testing.T) {
	testOnAllRuntimes(t, func(t *testing.T, rt container.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		agentName := "e2e-vol-readonly"

		t.Cleanup(func() {
			cleanupVolume(t, rt, agentName, "rodata")
		})

		workspace := createTestWorkspaceWithVolumes(t, agentName, []config.VolumeConfig{
			{Name: "rodata", Target: "/rodata", ReadOnly: true},
		})

		r, err := mgr.Create(ctx, run.Options{
			Name:      agentName,
			Workspace: workspace,
			Config: &config.Config{
				Name:  agentName,
				Agent: "e2e-test",
				Volumes: []config.VolumeConfig{
					{Name: "rodata", Target: "/rodata", ReadOnly: true},
				},
			},
			// Try to write to the readonly volume; capture the exit status
			Cmd: []string{"sh", "-c", "touch /rodata/test.txt 2>&1 && echo WRITE_OK || echo WRITE_FAILED"},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer mgr.Destroy(context.Background(), r.ID)

		if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := mgr.Wait(ctx, r.ID); err != nil {
			// Expected — write should fail
			t.Logf("Wait returned error (expected for readonly test): %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if err != nil {
			t.Fatalf("NewRunStore: %v", err)
		}
		logs, err := store.ReadLogs(0, 100)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}

		for _, entry := range logs {
			if strings.Contains(entry.Line, "WRITE_OK") {
				t.Errorf("Write succeeded on readonly volume — expected failure\nLogs: %v", logs)
				return
			}
		}

		// WRITE_FAILED or an error message means the readonly mount is working
		t.Log("Readonly volume correctly prevented writes")
	})
}

// TestVolumeIsolation is Docker-only: verifies that volumes for different agent names
// are isolated from each other (different Docker named volumes).
func TestVolumeIsolation(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	agent1 := "e2e-vol-iso-a"
	agent2 := "e2e-vol-iso-b"

	t.Cleanup(func() {
		cleanupVolume(t, nil, agent1, "state")
		cleanupVolume(t, nil, agent2, "state")
	})

	ws1 := createTestWorkspaceWithVolumes(t, agent1, []config.VolumeConfig{
		{Name: "state", Target: "/data"},
	})
	ws2 := createTestWorkspaceWithVolumes(t, agent2, []config.VolumeConfig{
		{Name: "state", Target: "/data"},
	})

	// Agent 1 writes a unique marker.
	// Use a command that captures diagnostics and doesn't fail fatally.
	writeCmd := strings.Join([]string{
		"echo DIAG_ID=$(id)",
		"echo DIAG_MOUNT=$(mount | grep /data || echo 'no /data mount')",
		"echo 'agent1-secret' > /data/marker.txt 2>&1 && echo WRITE_OK || echo WRITE_FAILED",
	}, "; ")

	r1, err := mgr.Create(ctx, run.Options{
		Name:      agent1,
		Workspace: ws1,
		Config: &config.Config{
			Name:  agent1,
			Agent: "e2e-test",
			Volumes: []config.VolumeConfig{
				{Name: "state", Target: "/data"},
			},
		},
		Cmd: []string{"sh", "-c", writeCmd},
	})
	if err != nil {
		t.Fatalf("Create agent1: %v", err)
	}

	if err := mgr.Start(ctx, r1.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start agent1: %v", err)
	}
	if err := mgr.Wait(ctx, r1.ID); err != nil {
		t.Logf("Wait agent1 returned error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Read agent1 logs to verify write and get diagnostics
	store1, err := storage.NewRunStore(storage.DefaultBaseDir(), r1.ID)
	if err != nil {
		t.Fatalf("NewRunStore agent1: %v", err)
	}
	logs1, err := store1.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs agent1: %v", err)
	}

	for _, entry := range logs1 {
		if strings.HasPrefix(entry.Line, "DIAG_") {
			t.Logf("Agent1: %s", entry.Line)
		}
	}

	writeSucceeded := false
	for _, entry := range logs1 {
		if strings.Contains(entry.Line, "WRITE_OK") {
			writeSucceeded = true
		}
	}
	if !writeSucceeded {
		t.Fatalf("Agent1 failed to write to volume\nLogs: %v", logs1)
	}

	if err := mgr.Destroy(ctx, r1.ID); err != nil {
		t.Fatalf("Destroy agent1: %v", err)
	}

	// Agent 2 should NOT see agent 1's data
	r2, err := mgr.Create(ctx, run.Options{
		Name:      agent2,
		Workspace: ws2,
		Config: &config.Config{
			Name:  agent2,
			Agent: "e2e-test",
			Volumes: []config.VolumeConfig{
				{Name: "state", Target: "/data"},
			},
		},
		Cmd: []string{"sh", "-c", "cat /data/marker.txt 2>&1 || echo FILE_NOT_FOUND"},
	})
	if err != nil {
		t.Fatalf("Create agent2: %v", err)
	}
	defer mgr.Destroy(context.Background(), r2.ID)

	if err := mgr.Start(ctx, r2.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start agent2: %v", err)
	}
	if err := mgr.Wait(ctx, r2.ID); err != nil {
		t.Logf("Wait agent2: %v (expected)", err)
	}

	time.Sleep(100 * time.Millisecond)

	store2, err := storage.NewRunStore(storage.DefaultBaseDir(), r2.ID)
	if err != nil {
		t.Fatalf("NewRunStore agent2: %v", err)
	}
	logs2, err := store2.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs agent2: %v", err)
	}

	for _, entry := range logs2 {
		if strings.Contains(entry.Line, "agent1-secret") {
			t.Errorf("Agent 2 could read agent 1's volume data — isolation violated\nLogs: %v", logs2)
			return
		}
	}
	t.Log("Volume isolation verified: agent 2 cannot see agent 1's data")
}

// createTestWorkspaceWithVolumes creates a test workspace with an agent.yaml
// that specifies volumes.
func createTestWorkspaceWithVolumes(t *testing.T, agentName string, volumes []config.VolumeConfig) string {
	t.Helper()

	dir := t.TempDir()

	var volLines string
	for _, v := range volumes {
		volLines += "  - name: " + v.Name + "\n"
		volLines += "    target: " + v.Target + "\n"
		if v.ReadOnly {
			volLines += "    readonly: true\n"
		}
	}

	yaml := "name: " + agentName + "\nagent: e2e-test\nversion: 1.0.0\nvolumes:\n" + volLines
	if err := os.WriteFile(dir+"/agent.yaml", []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile agent.yaml: %v", err)
	}

	return dir
}

// cleanupVolume removes a test volume by removing the host-backed directory.
func cleanupVolume(t *testing.T, _ container.Runtime, agentName, volumeName string) {
	t.Helper()

	volDir := config.VolumeDir(agentName, volumeName)
	if err := os.RemoveAll(volDir); err != nil {
		t.Logf("cleanup: failed to remove volume dir %s: %v", volDir, err)
	}
}
