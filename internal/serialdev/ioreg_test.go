package serialdev

import (
	"strings"
	"testing"
)

// Representative `ioreg -p IOUSB -l -w0` output: a hub with no serial child,
// an ESP32 with a serial number, and a CH340 clone without one.
const ioregSample = `+-o Root  <class IORegistryEntry, id 0x100000100, retain 15>
  +-o AppleT8103USBXHCI@01000000  <class AppleT8103USBXHCI, id 0x100000abc, retain 20>
    +-o USB2.0 Hub@01100000  <class IOUSBHostDevice, id 0x100000def, registered>
      | {
      |   "idProduct" = 10531
      |   "idVendor" = 1155
      |   "locationID" = 17825792
      | }
      +-o USB JTAG_serial debug unit@01110000  <class IOUSBHostDevice, id 0x100000e01, registered>
        | {
        |   "idProduct" = 4097
        |   "idVendor" = 12346
        |   "USB Serial Number" = "34:85:18:0D:1A:2C"
        |   "USB Product Name" = "USB JTAG/serial debug unit"
        |   "locationID" = 17891328
        | }
        +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100000e02, registered>
          +-o AppleUSBACM  <class AppleUSBACM, id 0x100000e03, registered>
            +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000e04, registered>
              {
                "IOCalloutDevice" = "/dev/cu.usbmodem34851801A2C1"
                "IODialinDevice" = "/dev/tty.usbmodem34851801A2C1"
              }
      +-o USB Serial@01120000  <class IOUSBHostDevice, id 0x100000f01, registered>
        | {
        |   "idProduct" = 29987
        |   "idVendor" = 6790
        |   "locationID" = 17956864
        | }
        +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100000f02, registered>
          +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000f03, registered>
            {
              "IOCalloutDevice" = "/dev/cu.usbserial-14220"
            }
`

func parseSample(t *testing.T) []Device {
	t.Helper()
	got, err := parseIoreg(strings.NewReader(ioregSample))
	if err != nil {
		t.Fatalf("parseIoreg: %v", err)
	}
	return got
}

func findByPath(devs []Device, path string) (Device, bool) {
	for _, d := range devs {
		if d.Path == path {
			return d, true
		}
	}
	return Device{}, false
}

func TestParseIoregExtractsIdentity(t *testing.T) {
	got := parseSample(t)
	d, ok := findByPath(got, "/dev/cu.usbmodem34851801A2C1")
	if !ok {
		t.Fatalf("ESP32 not found in %+v", got)
	}
	// ioreg reports IDs in decimal: 12346 = 0x303a, 4097 = 0x1001.
	if d.VID != "303a" || d.PID != "1001" {
		t.Fatalf("got %s:%s, want 303a:1001 converted from decimal", d.VID, d.PID)
	}
	if d.Serial != "34:85:18:0D:1A:2C" {
		t.Fatalf("Serial = %q, want the unquoted serial number", d.Serial)
	}
	if d.PortPath != "0x01110000" {
		t.Fatalf("PortPath = %q, want the hex locationID 0x01110000", d.PortPath)
	}
	if d.Description != "USB JTAG/serial debug unit" {
		t.Fatalf("Description = %q, want the product name", d.Description)
	}
}

func TestParseIoregSerialLessDeviceKeepsPortPath(t *testing.T) {
	got := parseSample(t)
	d, ok := findByPath(got, "/dev/cu.usbserial-14220")
	if !ok {
		t.Fatalf("CH340 not found in %+v", got)
	}
	if d.Serial != "" {
		t.Fatalf("Serial = %q, want empty", d.Serial)
	}
	if d.PortPath != "0x01120000" {
		t.Fatalf("PortPath = %q, want 0x01120000 — the only identity this device has", d.PortPath)
	}
	if d.VID != "1a86" || d.PID != "7523" {
		t.Fatalf("got %s:%s, want 1a86:7523", d.VID, d.PID)
	}
}

func TestParseIoregSkipsUSBDevicesWithNoSerialPort(t *testing.T) {
	// The hub is an IOUSBHostDevice but has no IOSerialBSDClient child.
	for _, d := range parseSample(t) {
		if d.VID == "0483" {
			t.Fatalf("hub should not be enumerated: %+v", d)
		}
	}
}

func TestParseIoregReturnsOnlySerialDevices(t *testing.T) {
	if got := parseSample(t); len(got) != 2 {
		t.Fatalf("got %d devices, want 2: %+v", len(got), got)
	}
}

func TestParseIoregEmptyInputIsNotAnError(t *testing.T) {
	got, err := parseIoreg(strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseIoreg on empty input: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no devices", got)
	}
}

func TestDecimalToHexIDPadsToFourDigits(t *testing.T) {
	if got := decimalToHexID("67"); got != "0043" {
		t.Fatalf("decimalToHexID(67) = %q, want 0043", got)
	}
}

func TestDecimalToHexIDRejectsNonNumeric(t *testing.T) {
	if got := decimalToHexID("not-a-number"); got != "" {
		t.Fatalf("decimalToHexID = %q, want empty so the device is skipped", got)
	}
}

func TestParseIoregPropertyHandlesTreePrefixes(t *testing.T) {
	key, val, ok := parseIoregProperty(`      |   "idVendor" = 1155`)
	if !ok || key != "idVendor" || val != "1155" {
		t.Fatalf("got (%q, %q, %v), want (idVendor, 1155, true)", key, val, ok)
	}
}

func TestParseIoregPropertyIgnoresNonPropertyLines(t *testing.T) {
	if _, _, ok := parseIoregProperty(`      | {`); ok {
		t.Fatal("brace line should not parse as a property")
	}
}
