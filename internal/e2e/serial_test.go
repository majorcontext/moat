//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/storage"
)

// =============================================================================
// Serial device E2E tests
//
// These tests need a real board attached to the host, named by
// MOAT_SERIAL_TEST_DEVICE (e.g. /dev/ttyACM0 or /dev/cu.usbmodem-14201).
// Without it they skip — CI has no serial hardware.
//
// They verify the full path the hardware rides on: enumerate → resolve →
// pin → register with the daemon → broker listener → container reaches the
// device via the MOAT_SERIAL_<NAME>_URL env var and drives it with
// pyserial over RFC2217.
//
// Set MOAT_SERIAL_TEST_ECHO=1 when the board runs firmware that echoes what
// it receives (any Arduino-style Serial loopback sketch); the data-path
// assertion is then exercised too. Without it the test checks everything
// moat is responsible for — open, RFC2217 negotiation, line settings,
// DTR/RTS — but not what the far end does with the bytes.
// =============================================================================

// serialTestScript is the pyserial client the container runs. It must work
// against a bare USB adapter (nothing to read) as well as an echoing board.
const serialTestScript = `set -e
python3 - <<'PY'
import os

import serial  # installed via the pip:pyserial build dependency

url = os.environ["MOAT_SERIAL_DUT_URL"]
print("url:", url)
assert url.startswith("rfc2217://"), url

s = serial.serial_for_url(url, timeout=3)
s.baudrate = 115200
s.bytesize = serial.EIGHTBITS
s.parity = serial.PARITY_NONE
s.stopbits = serial.STOPBITS_ONE
print("settings:", s.get_settings()["baudrate"])

# DTR/RTS are the ESP32 auto-reset lines — this is the part a pty cannot do.
s.dtr = True
s.rts = False
s.dtr = False
s.rts = True
print("dtr/rts: applied")

if os.environ.get("MOAT_SERIAL_TEST_ECHO") == "1":
    s.reset_input_buffer()
    s.write(b"moat-e2e\n")
    got = s.read(64)
    print("read-back:", len(got), "bytes")
    assert b"moat-e2e" in got, "board did not echo"
    print("data path: ok")
else:
    print("data path: skipped (set MOAT_SERIAL_TEST_ECHO=1)")

s.close()
print("SERIAL-E2E-OK")
PY`

// createSerialTestWorkspace returns a workspace whose moat.yaml requests the
// given device. The name "dut" keeps the env var MOAT_SERIAL_DUT_URL short.
func createSerialTestWorkspace(t *testing.T, vid, pid string) string {
	t.Helper()
	dir := t.TempDir()
	yaml := "name: serial-e2e\nagent: e2e-test\ndependencies: [python, pip:pyserial]\n" +
		"devices:\n  - name: dut\n    match: {usb: \"" + vid + ":" + pid + "\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile moat.yaml: %v", err)
	}
	return dir
}

// enumerateSerialDevice finds the attached device at path and returns its
// USB identity, so the test targets the actual board rather than hardcoded
// IDs that may describe a different one.
func enumerateSerialDevice(t *testing.T, dev string) serialdev.Device {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	devices, err := serialdev.NewEnumerator().List(ctx)
	if err != nil {
		t.Fatalf("enumerating serial devices: %v", err)
	}
	for _, d := range devices {
		if d.Path == dev {
			return d
		}
	}
	t.Fatalf("%s is not an attached USB serial device. Attached: %s", dev, describeAttachedSerial(devices))
	return serialdev.Device{}
}

func describeAttachedSerial(devices []serialdev.Device) string {
	if len(devices) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(devices))
	for _, d := range devices {
		parts = append(parts, d.Path+" ("+d.VID+":"+d.PID+")")
	}
	return strings.Join(parts, ", ")
}

// TestSerialDeviceEndToEnd runs the full serial path against real hardware.
//
// The run resolves and pins the device named by MOAT_SERIAL_TEST_DEVICE, the
// daemon opens an RFC2217 listener for it, and the container opens the
// resulting URL with pyserial, negotiates line settings, and toggles DTR/RTS
// — the ESP32 auto-reset lines, which is exactly what flashing needs and a
// plain pty cannot carry. After the container exits, the run's devices.jsonl
// must show the attach/detach pair for the session.
func TestSerialDeviceEndToEnd(t *testing.T) {
	// Check for hardware before creating a runtime: without the env var this
	// test has nothing to do, and reaching for Docker first would fail on
	// hosts that lack a configured runtime (or gVisor) even though the skip
	// was always going to fire.
	if os.Getenv("MOAT_SERIAL_TEST_DEVICE") == "" {
		t.Skip("MOAT_SERIAL_TEST_DEVICE not set — skipping hardware serial test")
	}

	testOnAllRuntimes(t, func(t *testing.T, _ container.Runtime) {
		devPath := os.Getenv("MOAT_SERIAL_TEST_DEVICE")

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		dev := enumerateSerialDevice(t, devPath)
		t.Logf("device: %s %s:%s serial=%q port=%s", dev.Path, dev.VID, dev.PID, dev.Serial, dev.PortPath)

		// A prior pin on this name (from an earlier test run against a
		// different board) would make resolution fail by design. This test
		// running is the approval, so forget any stale pin first.
		pins, err := serialdev.OpenPinStore(serialdev.DefaultPinPath())
		if err != nil {
			t.Fatalf("OpenPinStore: %v", err)
		}
		if err := pins.Forget("dut"); err != nil {
			t.Fatalf("forgetting stale pin: %v", err)
		}

		mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: boolPtr(true)})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		defer mgr.Close()

		r, err := mgr.Create(ctx, run.Options{
			Name:      "e2e-serial-" + dev.VID,
			Workspace: createSerialTestWorkspace(t, dev.VID, dev.PID),
			Config: &config.Config{
				Name:         "serial-e2e",
				Dependencies: []string{"python", "pip:pyserial"},
				Devices:      []config.DeviceEntry{{Name: "dut", Match: config.DeviceMatch{USB: dev.VID + ":" + dev.PID}}},
				Network:      config.NetworkConfig{Policy: "permissive"},
			},
			Cmd: []string{"sh", "-c", serialTestScript},
		})
		if err != nil {
			t.Fatalf("Create with device: %v", err)
		}
		defer mgr.Destroy(context.Background(), r.ID)

		if err := mgr.Start(ctx, r.ID); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := mgr.Wait(ctx, r.ID); err != nil {
			t.Logf("Wait: %v", err)
		}

		time.Sleep(200 * time.Millisecond)
		logs := readRunLogs(t, r.ID)
		t.Logf("Container output:\n%s", logs)

		if !strings.Contains(logs, "SERIAL-E2E-OK") {
			t.Errorf("serial test did not complete inside the container.\nLogs: %s", logs)
		}

		// The run must have recorded the device session in its storage.
		assertSerialDeviceEvents(t, r.ID)
	})
}

// assertSerialDeviceEvents checks the run's devices.jsonl recorded an
// attach/detach pair for the device session, failing the test otherwise.
func assertSerialDeviceEvents(t *testing.T, runID string) {
	t.Helper()
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), runID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	events, err := store.ReadDeviceEvents()
	if err != nil {
		t.Fatalf("ReadDeviceEvents: %v", err)
	}
	var sawAttach, sawDetach bool
	for _, ev := range events {
		t.Logf("device event: %s %s %s", ev.Device, ev.Kind, ev.Detail)
		if ev.Device != "dut" {
			continue
		}
		switch ev.Kind {
		case "attach":
			sawAttach = true
		case "detach":
			sawDetach = true
		}
	}
	if !sawAttach {
		t.Errorf("no attach event for device dut in run %s — the broker session never ran", runID)
	}
	if !sawDetach {
		t.Errorf("no detach event for device dut in run %s — the session did not close cleanly", runID)
	}
}
