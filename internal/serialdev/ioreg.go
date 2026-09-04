package serialdev

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// parseIoreg extracts serial devices from the output of
// `ioreg -r -p IOService -l -w0 -c IOUSBHostDevice` (see
// enumerate_darwin.go for why that invocation).
//
// That output is an indented tree. USB devices carry the identity attributes
// (idVendor, idProduct, USB Serial Number, locationID) but not the device node;
// the node appears on an IOSerialBSDClient descendant several levels below:
//
//	+-o USB Single Serial@01110000  <class IOUSBHostDevice, ...>
//	  | {
//	  |   "idProduct" = 29987
//	  |   "idVendor" = 6790
//	  |   "USB Serial Number" = "0001"
//	  |   "locationID" = 17891328
//	  | }
//	  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, ...>
//	    +-o AppleUSBACM  <class AppleUSBACM, ...>
//	      +-o IOSerialBSDClient  <class IOSerialBSDClient, ...>
//	        {
//	          "IOCalloutDevice" = "/dev/cu.usbserial-0001"
//	        }
//
// So the parser keeps a stack of open USB devices keyed by tree depth and
// attaches a callout device to the nearest enclosing one.
//
// Devices are dropped when they cannot be matched or pinned: no USB ID, or —
// for the serial list — no serial child.
func parseIoreg(r io.Reader) (serial []Device, err error) {
	all, err := parseIoregAll(r)
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if d.Path != "" {
			serial = append(serial, d)
		}
	}
	return serial, nil
}

// parseIoregUSB extracts non-serial USB devices from the same output: every
// USB device with an identity but no serial child, except hubs — showing the
// host's own hubs would be noise, not an answer to "what is plugged in".
// Hubs are dropped by USB class (9) and, as a backstop, by product name
// (hubByName): real Macs carry internal hub controllers ("USB2 Controller
// Hub", e.g. 0424:7240) whose ioreg blocks report no class-9 bDeviceClass,
// so the class check alone lets them through.
func parseIoregUSB(r io.Reader) ([]Device, error) {
	all, err := parseIoregAll(r)
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, d := range all {
		if d.Path == "" && !d.isHub && !hubByName(d.Description) {
			out = append(out, d)
		}
	}
	return out, nil
}

// parseIoregAll walks ioreg's tree and returns every USB device with a USB ID,
// serial or not. It is the shared core of parseIoreg and parseIoregUSB.
//
// A USB device with several serial clients (a dual-UART bridge such as an
// FT2232H) yields one Device per client: the first takes the frame's Device,
// and each later one clones it with its own path and interface number. Without
// that, the clients overwrite each other and all but the last port are
// unreachable.
func parseIoregAll(r io.Reader) ([]Device, error) {
	type frame struct {
		depth int
		dev   *Device
		// extra holds the Devices created for serial clients after the
		// first: a multi-interface bridge has one tty per interface.
		extra     []Device
		ifaceNum  string // interface number of the nearest IOUSBHostInterface@N below this device
		ifaceSeen bool
	}
	var (
		stack []frame
		out   []Device
	)

	// flush emits every stacked device, serial or not; the split into serial
	// and non-serial lists is the callers' job.
	flushTo := func(depth int) {
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			out = append(out, *f.dev)
			out = append(out, f.extra...)
		}
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	// A property block belongs to the most recent `+-o` line, so the
	// device's own block is the one directly under its own `+-o` line.
	// Deeper `+-o` lines (AppleUSBCDCCompositeDevice, AppleUSBACM,
	// IOUSBHostInterface, user clients) open their own property blocks under
	// the same tree prefix, and without this guard their properties write
	// into the enclosing device, last-write-wins: a downstream port's
	// locationID would corrupt the parent's PortPath, which is the identity
	// fallback for serial-less devices. The one descendant value the parser
	// does want — IOCalloutDevice, the tty path — is let through explicitly.
	ownProps := false
	for sc.Scan() {
		line := sc.Text()
		if idx := strings.Index(line, "+-o "); idx >= 0 {
			// A nested IOUSBHostInterface@N under the open USB device names the
			// interface the serial clients below it belong to. It is the only
			// discriminator between the UARTs of a dual-interface bridge.
			if len(stack) > 0 && strings.Contains(line, "<class IOUSBHostInterface") {
				if n := interfaceAt(line); n != "" {
					stack[len(stack)-1].ifaceNum = n
					stack[len(stack)-1].ifaceSeen = true
				}
			}
			flushTo(idx)
			ownProps = false
			if strings.Contains(line, "<class IOUSBHostDevice") {
				stack = append(stack, frame{depth: idx, dev: &Device{}})
				// This `+-o` line is the device itself, so the property block
				// that follows is its own.
				ownProps = true
			}
			continue
		}
		if len(stack) == 0 {
			continue
		}
		key, val, ok := parseIoregProperty(line)
		if !ok {
			continue
		}
		// IOCalloutDevice only appears on IOSerialBSDClient descendants, so it
		// is exempt from the own-block rule — the whole point of the parser
		// is to attach the descendant's tty to the nearest enclosing device.
		if key != "IOCalloutDevice" && !ownProps {
			continue
		}
		f := &stack[len(stack)-1]
		cur := f.dev
		switch key {
		case "idVendor":
			cur.VID = decimalToHexID(val)
		case "idProduct":
			cur.PID = decimalToHexID(val)
		case "USB Serial Number":
			cur.Serial = strings.Trim(val, `"`)
		case "locationID":
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				cur.PortPath = fmt.Sprintf("0x%08x", n)
			}
		case "bDeviceClass":
			// 9 = hub (USB class code). parseIoregUSB drops them; a hub is host
			// infrastructure, not a plugged-in device anyone is looking for.
			if strings.TrimSpace(val) == "9" {
				cur.isHub = true
			}
		case "USB Product Name":
			// macOS sanitizes this one (the S3's "USB JTAG/serial debug unit"
			// renders as "USB JTAG_serial debug unit"), so it only fills in
			// when the real string is absent.
			if cur.Description == "" {
				cur.Description = strings.Trim(val, `"`)
			}
		case "kUSBProductString":
			// ioreg prints nothing after `=` when the string is non-ASCII
			// (a RØDE NT-USB Mini lists with a blank description because of
			// it). An empty value must not clobber the sanitized product
			// name above — the `+-o` node name label is not read.
			if s := strings.Trim(val, `"`); s != "" {
				cur.Description = s
			}
		case "IOCalloutDevice":
			// Attach to the nearest enclosing USB device, not the innermost
			// stack frame, since IOSerialBSDClient is not itself a USB device.
			path := strings.Trim(val, `"`)
			if cur.Path == "" {
				cur.Path = path
				if f.ifaceSeen {
					cur.Interface = f.ifaceNum
				}
				break
			}
			// A second serial client on the same USB device: another UART of
			// the same bridge. Clone the device so both ports are enumerable
			// and pinnable; the interface number tells them apart.
			clone := *cur
			clone.Path = path
			if f.ifaceSeen {
				clone.Interface = f.ifaceNum
			}
			f.extra = append(f.extra, clone)
		}
	}
	if err := sc.Err(); err != nil {
		// A single oversized line (real ioreg output contains 300 KB lines)
		// skips that line rather than failing the whole enumeration — "no
		// devices at all" is the worst possible report for a long line the
		// user cannot see. bufio's scanner already advanced past it.
		if !errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("reading ioreg output: %w", err)
		}
	}
	flushTo(0)

	// Drop anything that cannot be pinned: no USB ID means it cannot be
	// matched, and a serial-less device with no locationID has no identity
	// at all — a pin for it would approve any device with the same USB ID
	// (Verify fails closed on such a pin, so it must not be created).
	kept := out[:0]
	for _, d := range out {
		if d.VID != "" && d.PID != "" && (d.Serial != "" || d.PortPath != "") {
			kept = append(kept, d)
		}
	}
	return kept, nil
}

// interfaceAt pulls the trailing @N off an ioreg tree line naming
// IOUSBHostInterface@N — the USB interface number of the clients below it.
func interfaceAt(line string) string {
	at := strings.LastIndex(line, "@")
	if at < 0 {
		return ""
	}
	rest := line[at+1:]
	end := strings.IndexAny(rest, " ,")
	if end >= 0 {
		rest = rest[:end]
	}
	if n, err := strconv.Atoi(rest); err == nil {
		return strconv.Itoa(n)
	}
	return ""
}

// parseIoregProperty pulls `"key" = value` out of a property line, which is
// prefixed with tree-drawing characters and whitespace.
func parseIoregProperty(line string) (key, val string, ok bool) {
	s := strings.TrimLeft(line, " |")
	if !strings.HasPrefix(s, `"`) {
		return "", "", false
	}
	end := strings.Index(s[1:], `"`)
	if end < 0 {
		return "", "", false
	}
	key = s[1 : 1+end]
	rest := strings.TrimSpace(s[1+end+1:])
	if !strings.HasPrefix(rest, "=") {
		return "", "", false
	}
	return key, strings.TrimSpace(strings.TrimPrefix(rest, "=")), true
}

// decimalToHexID converts ioreg's decimal USB IDs to the 4-digit lowercase hex
// form used everywhere else (moat.yaml, sysfs, lsusb). It returns "" for
// values that are unparseable or out of the 16-bit USB ID range: `%04x` on a
// larger value would emit 5+ digits that match no config, so the device is
// dropped either way — but with the range check it is dropped for a reason the
// caller can state.
func decimalToHexID(s string) string {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil || n > 0xffff {
		return ""
	}
	return fmt.Sprintf("%04x", n)
}
