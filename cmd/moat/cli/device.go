package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

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

func init() {
	deviceCmd.AddCommand(deviceListCmd)
	deviceCmd.AddCommand(deviceForgetCmd)
	rootCmd.AddCommand(deviceCmd)
}

func listDevices(cmd *cobra.Command, _ []string) error {
	pins, err := serialdev.OpenPinStore(serialdev.DefaultPinPath())
	if err != nil {
		return err
	}
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
		fmt.Fprintln(w, "No serial devices attached.")
		fmt.Fprintln(w)
		if len(usbDevices) == 0 {
			fmt.Fprintln(w, "Plug in a device and run this again. USB-serial adapters and dev boards")
			fmt.Fprintln(w, "(ESP32, Arduino, RP2040) appear here once connected.")
		} else {
			// Something IS plugged in — say that, or the list above reads as
			// the device not being detected.
			fmt.Fprintln(w, "USB devices are attached, but none has a serial interface — they are")
			fmt.Fprintln(w, "listed below. USB-serial adapters and dev boards (ESP32, Arduino,")
			fmt.Fprintln(w, "RP2040) appear here once connected.")
		}
		if err := printUSBDevices(w, usbDevices); err != nil {
			return err
		}
		return printOrphanPins(w, pins, devices)
	}

	sort.Slice(devices, func(i, j int) bool { return devices[i].Path < devices[j].Path })

	// No ui styling inside the tabwriter: ANSI codes break column alignment.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	// "SERIAL NUMBER", not "SERIAL": the word serial appears twice in this
	// output with different meanings — the hardware serial number in this
	// column, and the name you pick for the `name:` key in the snippet
	// below. Spelling the column out keeps them apart.
	fmt.Fprintln(tw, "DEVICE\tUSB ID\tSERIAL NUMBER\tPIN\tDESCRIPTION")
	for _, d := range devices {
		fmt.Fprintf(tw, "%s\t%s:%s\t%s\t%s\t%s\n",
			d.Path, d.VID, d.PID, serialOrDash(d), pinState(d, pins), descriptionOrDash(d))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Pick a name for the device and add it to moat.yaml. The name is")
	fmt.Fprintln(w, "yours to choose — it becomes MOAT_SERIAL_<NAME>_URL inside the run:")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  devices:\n    - name: %s\n      match: {usb: %q}\n",
		suggestedName(devices[0]), devices[0].VID+":"+devices[0].PID)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The first run that uses the device pins it to this hardware; the PIN column")
	fmt.Fprintln(w, "shows the name it is pinned under.")

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
	fmt.Fprintln(w, "Other USB devices attached (no serial interface — cannot use devices:):")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "USB ID\tSERIAL NUMBER\tDESCRIPTION")
	for _, d := range usbDevices {
		fmt.Fprintf(tw, "%s:%s\t%s\t%s\n", d.VID, d.PID, serialNumberOrDash(d), descriptionOrDash(d))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Serial-only tools cannot reach these. A dongle that streams over USB bulk")
	fmt.Fprintln(w, "transfers (SDRs like the RTL2832U) belongs on the host, with the container")
	fmt.Fprintln(w, "connecting over the network — see examples/serial-sdr in the moat repo.")
	return nil
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
	fmt.Fprintln(w, "Pinned devices that are not attached:")
	for _, p := range orphans {
		id := p.Serial
		if id == "" {
			id = "port " + p.PortPath
		}
		fmt.Fprintf(w, "  %s (%s:%s, %s)\n", p.Name, p.VID, p.PID, id)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run `moat device forget <name>` to approve different hardware for one.")
	return nil
}

// pinState describes a device's approval status.
func pinState(d serialdev.Device, pins []serialdev.Pin) string {
	for _, p := range pins {
		if !strings.EqualFold(p.VID, d.VID) || !strings.EqualFold(p.PID, d.PID) {
			continue
		}
		if p.Verify(d) == nil {
			return p.Name
		}
		// Same model, different device: this is the case a run would reject.
		return "MISMATCH (" + p.Name + ")"
	}
	return "-"
}

func serialOrDash(d serialdev.Device) string {
	if d.Serial == "" {
		// Such a device is pinned by physical port instead; say so here rather
		// than letting the user discover it from a confusing error later.
		return "- (pins by port " + d.PortPath + ")"
	}
	return d.Serial
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

func forgetDevice(cmd *cobra.Command, args []string) error {
	name := args[0]
	pins, err := serialdev.OpenPinStore(serialdev.DefaultPinPath())
	if err != nil {
		return err
	}
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
