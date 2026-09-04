package serialdev

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// parseIoreg extracts serial devices from the output of
// `ioreg -p IOUSB -l -w0`.
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
// USB device with an identity but no serial child, except hubs (USB class 9) —
// showing the host's own hubs would be noise, not an answer to "what is
// plugged in".
func parseIoregUSB(r io.Reader) ([]Device, error) {
	all, err := parseIoregAll(r)
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, d := range all {
		if d.Path == "" && !d.isHub {
			out = append(out, d)
		}
	}
	return out, nil
}

// parseIoregAll walks ioreg's tree and returns every USB device with a USB ID,
// serial or not. It is the shared core of parseIoreg and parseIoregUSB.
func parseIoregAll(r io.Reader) ([]Device, error) {
	type frame struct {
		depth int
		dev   *Device
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
		}
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if idx := strings.Index(line, "+-o "); idx >= 0 {
			flushTo(idx)
			if strings.Contains(line, "<class IOUSBHostDevice") {
				stack = append(stack, frame{depth: idx, dev: &Device{}})
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
		cur := stack[len(stack)-1].dev
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
			cur.Description = strings.Trim(val, `"`)
		case "IOCalloutDevice":
			// Attach to the nearest enclosing USB device, not the innermost
			// stack frame, since IOSerialBSDClient is not itself a USB device.
			stack[len(stack)-1].dev.Path = strings.Trim(val, `"`)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading ioreg output: %w", err)
	}
	flushTo(0)

	// Drop anything without a USB ID: it cannot be matched or pinned.
	kept := out[:0]
	for _, d := range out {
		if d.VID != "" && d.PID != "" {
			kept = append(kept, d)
		}
	}
	return kept, nil
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
// form used everywhere else (moat.yaml, sysfs, lsusb).
func decimalToHexID(s string) string {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%04x", n)
}
