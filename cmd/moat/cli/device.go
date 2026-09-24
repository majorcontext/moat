package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/ui"
)

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Inspect and manage serial devices",
	Long: `Inspect the serial devices attached to this machine and manage which ones
moat runs are approved to use.

A run gets access to a device by declaring it in moat.yaml:

  devices:
    - name: esp32
      match: {usb: "303a:1001"}

The first run to use a device records its identity. Later runs must present the
same device, so swapping in different hardware fails rather than silently
flashing the wrong board.`,
	RunE: listDevices,
}

var deviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List attached serial devices",
	Long: `List the serial devices attached to this machine, with the USB IDs to put in
moat.yaml and whether each is already pinned to a device name.`,
	RunE: listDevices,
}

var deviceForgetCmd = &cobra.Command{
	Use:   "forget <name>",
	Short: "Forget a device pin so the next run re-approves it",
	Long: `Remove the recorded identity for a device name.

Use this after deliberately swapping hardware: the next run that uses the name
approves whatever device is attached then, and pins it.`,
	Args: cobra.ExactArgs(1),
	RunE: forgetDevice,
}

var deviceRenameCmd = &cobra.Command{
	Use:   "rename <old> <new>",
	Short: "Rename a device pin, keeping the approved hardware",
	Long: `Move a device pin to a different name.

The approval stays with the same hardware — this renames the pin, it does not
re-approve anything. Use it when you change a ` + "`name:`" + ` in moat.yaml: without it
the new name is unpinned, the next run approves the device again under that
name, and the old pin stays behind for the same hardware.

To approve different hardware for a name, use ` + "`moat device forget`" + ` instead.`,
	Args: cobra.ExactArgs(2),
	RunE: renameDevice,
}

func init() {
	deviceCmd.AddCommand(deviceListCmd)
	deviceCmd.AddCommand(deviceForgetCmd)
	deviceCmd.AddCommand(deviceRenameCmd)
	rootCmd.AddCommand(deviceCmd)
}

func listDevices(cmd *cobra.Command, _ []string) error {
	pins, err := serialdev.OpenPinStore(serialdev.DefaultPinPath())
	if err != nil {
		return err
	}
	defer pins.Close() //nolint:errcheck // read-only; an error on close has no consequence
	enum := serialdev.NewEnumerator()
	devices, err := enum.List(cmd.Context())
	if err != nil {
		return fmt.Errorf("listing serial devices: %w", err)
	}
	// Non-serial USB devices are a diagnostic surface, not a feature this
	// command acts on: a failure to list them must not hide the serial list,
	// or the pinned-device information under it.
	var usbDevices []serialdev.Device
	if u, ok := enum.(serialdev.USBEnumerator); ok {
		if usbDevices, err = u.ListUSB(cmd.Context()); err != nil {
			ui.Warnf("listing USB devices: %v", err)
			usbDevices = nil
		}
	}
	allPins, err := pins.List()
	if err != nil {
		return err
	}
	return printDevices(cmd.OutOrStdout(), devices, usbDevices, allPins)
}

// printDevices renders the device table. Splitting it out keeps the formatting
// testable without hardware.
func printDevices(w io.Writer, devices []serialdev.Device, usbDevices []serialdev.Device, pins []serialdev.Pin) error {
	if len(devices) == 0 {
		// Say whether anything is plugged in at all: with a USB table below,
		// a bare "none attached" reads as the device not being detected.
		if len(usbDevices) == 0 {
			fmt.Fprintln(w, "No serial devices attached.")
		} else {
			fmt.Fprintln(w, "No serial devices attached — no USB device below has a serial interface.")
			// Directly under the status it explains, not stranded at the end.
			fmt.Fprintln(w, "USB-serial adapters and dev boards (ESP32, Arduino, RP2040) appear here once attached.")
		}
		if err := printUSBDevices(w, usbDevices); err != nil {
			return err
		}
		if err := printOrphanPins(w, pins, devices); err != nil {
			return err
		}
		if len(usbDevices) == 0 {
			// Only say "plug one in" when nothing is attached; saying it to
			// someone holding a plugged-in device is the original complaint.
			fmt.Fprintln(w)
			fmt.Fprintln(w, "Plug in a USB-serial adapter or dev board (ESP32, Arduino, RP2040) and run this again.")
		}
		return nil
	}

	sort.Slice(devices, func(i, j int) bool { return devices[i].Path < devices[j].Path })

	// No ui styling inside the tabwriter: ANSI codes break column alignment.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	// "SERIAL NUMBER", not "SERIAL": the word serial appears twice in this
	// output with different meanings — the hardware serial number in this
	// column, and the name you pick for the `name:` key in the snippet
	// below. Spelling the column out keeps them apart.
	fmt.Fprintln(tw, "DEVICE\tUSB ID\tIFACE\tSERIAL NUMBER\tPIN\tDESCRIPTION")
	for _, d := range devices {
		fmt.Fprintf(tw, "%s\t%s:%s\t%s\t%s\t%s\t%s\n",
			d.Path, d.VID, d.PID, interfaceOrDash(d), serialOrDash(d), pinState(d, pins), descriptionOrDash(d))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	// Suggest the name the device is already pinned to, if any. Deriving a
	// fresh name from the USB description would propose a rename to a user who
	// has already approved this hardware — and a rename silently creates a
	// second pin for it rather than moving the first.
	name := pinnedName(devices[0], pins)
	if name == "" {
		name = suggestedName(devices[0])
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Add to moat.yaml:")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  devices:\n    - name: %s\n      match: {usb: %q}\n",
		name, devices[0].VID+":"+devices[0].PID)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "The run gets %s.\n", run.SerialEnvVarName(name))
	if pinnedName(devices[0], pins) == "" {
		// Only worth saying while it is still true. Once the hardware is
		// pinned, the PIN column says so and repeating it at the bottom of the
		// output describes something that already happened.
		fmt.Fprintln(w, "The first run pins this hardware to that name, shown in PIN.")
	}

	if err := printUSBDevices(w, usbDevices); err != nil {
		return err
	}
	return printOrphanPins(w, pins, devices)
}

// printUSBDevices renders the non-serial USB section. These devices cannot go
// through `devices:` — the serial broker only serves tty-backed hardware — so
// the section exists to tell a user whose SDR dongle "vanished" that moat sees
// it, and where its path actually is. Empty input prints nothing.
func printUSBDevices(w io.Writer, usbDevices []serialdev.Device) error {
	if len(usbDevices) == 0 {
		return nil
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Other USB devices (no serial interface, not usable with devices:)")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "USB ID\tSERIAL NUMBER\tDESCRIPTION")
	for _, d := range usbDevices {
		fmt.Fprintf(tw, "%s:%s\t%s\t%s\n", d.VID, d.PID, serialNumberOrDash(d), descriptionOrDash(d))
	}
	// No advice here. The rows are a keyboard, a camera, a LAN adapter as often
	// as they are an SDR, and there is no single thing to do about them — the
	// header already says the one fact that applies to all of them. Naming a
	// specific escape hatch overfits to whichever device prompted the section
	// and is wrong for the rest.
	return tw.Flush()
}

func serialNumberOrDash(d serialdev.Device) string {
	if d.Serial == "" {
		return "-"
	}
	return d.Serial
}

// printOrphanPins reports pins whose device is not attached, so a stale pin is
// visible rather than surfacing later as a confusing run failure.
func printOrphanPins(w io.Writer, pins []serialdev.Pin, attached []serialdev.Device) error {
	var orphans []serialdev.Pin
	for _, p := range pins {
		found := false
		for _, d := range attached {
			if p.Verify(d) == nil {
				found = true
				break
			}
		}
		if !found {
			orphans = append(orphans, p)
		}
	}
	if len(orphans) == 0 {
		return nil
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].Name < orphans[j].Name })

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Pinned but not attached — moat device forget <name> to re-approve")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tUSB ID\tSERIAL NUMBER")
	for _, p := range orphans {
		id := p.Serial
		if id == "" {
			id = "port " + p.PortPath
		}
		fmt.Fprintf(tw, "%s\t%s:%s\t%s\n", p.Name, p.VID, p.PID, id)
	}
	return tw.Flush()
}

// pinnedName returns the name this device is already pinned to, or "".
func pinnedName(d serialdev.Device, pins []serialdev.Pin) string {
	for _, p := range pins {
		if p.Verify(d) == nil {
			return p.Name
		}
	}
	return ""
}

// pinState describes a device's approval status.
func pinState(d serialdev.Device, pins []serialdev.Pin) string {
	// A pin that matches on USB ID but fails Verify is only conclusive if no
	// other pin verifies: the ports of one bridge share a USB ID, so pin "b"
	// failing against port A just means port A is pin "a".
	mismatch := ""
	for _, p := range pins {
		if !strings.EqualFold(p.VID, d.VID) || !strings.EqualFold(p.PID, d.PID) {
			continue
		}
		if p.Verify(d) == nil {
			return p.Name
		}
		if mismatch == "" {
			mismatch = p.Name
		}
	}
	if mismatch == "" {
		return "-"
	}
	return "MISMATCH (" + mismatch + ")"
}

func serialOrDash(d serialdev.Device) string {
	if d.Serial == "" {
		// Such a device is pinned by physical port instead; say so here rather
		// than letting the user discover it from a confusing error later.
		return "- (pins by port " + d.PortPath + ")"
	}
	return d.Serial
}

// interfaceOrDash renders the USB interface number. A bridge exposing several
// UARTs over one USB device lists one row per port with the same IDs, and the
// interface number is what tells them apart in moat.yaml's interface selector.
func interfaceOrDash(d serialdev.Device) string {
	if d.Interface == "" {
		return "-"
	}
	return d.Interface
}

func descriptionOrDash(d serialdev.Device) string {
	if d.Description == "" {
		return "-"
	}
	return d.Description
}

// suggestedName proposes a moat.yaml device name from the product string.
func suggestedName(d serialdev.Device) string {
	name := strings.ToLower(d.Description)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '/':
			b.WriteRune('-')
		}
	}
	trimmed := strings.Trim(b.String(), "-")
	for strings.Contains(trimmed, "--") {
		trimmed = strings.ReplaceAll(trimmed, "--", "-")
	}
	if trimmed == "" {
		return "mydevice"
	}
	return trimmed
}

func renameDevice(cmd *cobra.Command, args []string) error {
	from, to := args[0], args[1]
	// Validate before touching the store: a name that moat.yaml will reject is
	// a pin nothing can ever use, and the error belongs here rather than at the
	// next run.
	if !config.ValidDeviceName(to) {
		return fmt.Errorf("%q is not a usable device name\n"+
			"  Names must start with a letter or digit and contain only lowercase letters, digits, - and _\n"+
			"  The name becomes %s inside the run", to, run.SerialEnvVarName("<name>"))
	}

	pins, err := serialdev.OpenPinStore(serialdev.DefaultPinPath())
	if err != nil {
		return err
	}
	defer pins.Close() //nolint:errcheck // the process exits with the command

	switch err := pins.Rename(from, to); {
	case err == nil:
	case errors.Is(err, serialdev.ErrPinNotFound):
		return fmt.Errorf("no device named %q is pinned\n"+
			"  Run `moat device list` to see which names are in use", from)
	case errors.Is(err, serialdev.ErrPinNameTaken):
		return fmt.Errorf("a device is already pinned as %q\n"+
			"  Run `moat device forget %s` first if you mean to replace it", to, to)
	default:
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s Renamed %s to %s. Update the `name:` in moat.yaml to match; the run will get %s.\n",
		ui.OKTag(), ui.Bold(from), ui.Bold(to), run.SerialEnvVarName(to))
	return nil
}

func forgetDevice(cmd *cobra.Command, args []string) error {
	name := args[0]
	pins, err := serialdev.OpenPinStore(serialdev.DefaultPinPath())
	if err != nil {
		return err
	}
	defer pins.Close() //nolint:errcheck // the process exits with the command
	if _, ok, err := pins.Get(name); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("no device named %q is pinned\n"+
			"  Run `moat device list` to see which names are in use", name)
	}
	if err := pins.Forget(name); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s Forgot device %s. The next run that uses it will approve whatever is attached.\n",
		ui.OKTag(), ui.Bold(name))
	return nil
}
