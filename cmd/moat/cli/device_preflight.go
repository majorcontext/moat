// cmd/moat/cli/device_preflight.go
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/ui"
)

// devicePreflight carries what preflightDevices needs beyond the device list.
// The zero value enumerates real hardware and reads the default pin store,
// which is what the run path wants; tests substitute fakes.
type devicePreflight struct {
	// enum defaults to the real host enumerator.
	enum serialdev.Enumerator
	// pinPath defaults to serialdev.DefaultPinPath().
	pinPath string
	// out is where findings and the consent notice go.
	out io.Writer
}

// preflightDevices resolves the run's serial devices before anything is
// created, mirroring the grant pre-flight: a missing or mismatched device is
// reported with every other finding (and its fix command) instead of one at
// a time from inside manager.Create, and a device about to be approved on
// first use gets a consent notice — approving a device hands the run full
// control of that hardware.
//
// Findings here are advisory: manager.Create re-resolves devices through the
// same code (resolveDevices backs both), so a device that disappears between
// pre-flight and create still fails the run. The pre-flight exists so the
// failure is readable and nothing has been built yet.
//
// Returns false when a finding says the run cannot start.
func preflightDevices(ctx context.Context, devices []config.DeviceEntry, pf devicePreflight) bool {
	out := pf.out
	if out == nil {
		out = os.Stderr
	}
	enum := pf.enum
	if enum == nil {
		enum = serialdev.NewEnumerator()
	}
	pinPath := pf.pinPath
	if pinPath == "" {
		pinPath = serialdev.DefaultPinPath()
	}
	pins, err := serialdev.OpenPinStore(pinPath)
	if err != nil {
		// The Create gate opens the same store and reports the error with a
		// path to fixing it; duplicating that here would only risk drift.
		return true
	}
	defer pins.Close() //nolint:errcheck // read-only; an error on close has no consequence

	if missing := run.DetectMissingDevices(ctx, devices, enum, pins); len(missing) > 0 {
		fmt.Fprintln(out, run.FormatMissingDevices(missing))
		for _, m := range missing {
			if m.FixCommand != "" {
				fmt.Fprintf(out, "  %s: %s\n", m.Name, m.FixCommand)
			}
		}
		return false
	}

	if newPins := run.DetectNewPins(ctx, devices, enum, pins); len(newPins) > 0 {
		printConsentNotice(out, newPins)
	}
	return true
}

// printConsentNotice states what first use of each device approves. The
// design pins the wording for serial-less devices: the pin approves whatever
// is plugged into that port, not that particular device.
func printConsentNotice(w io.Writer, newPins []run.NewPin) {
	fmt.Fprintf(w, "%s Approving serial device%s for the first run:\n",
		ui.WarnTag(), devicePlural(len(newPins)))
	for _, p := range newPins {
		if p.ByPortPath {
			fmt.Fprintf(w, "  %s %s:%s (no serial number — pins whatever is plugged into port %s, not this unit)\n",
				p.Name, p.VID, p.PID, p.PortPath)
			continue
		}
		fmt.Fprintf(w, "  %s %s:%s (serial %s)\n", p.Name, p.VID, p.PID, p.Serial)
	}
	fmt.Fprintln(w, "  This is trust-on-first-use: the run gets full control of the hardware —")
	fmt.Fprintln(w, "  it can read, reflash, or brick the device. Later runs must present the same")
	fmt.Fprintln(w, "  device or fail until you run `moat device forget`.")
}

// devicePlural renders the noun for a count of devices.
func devicePlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
