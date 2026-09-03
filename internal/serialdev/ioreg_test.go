package serialdev

import (
	"strings"
	"testing"
)

// Real `ioreg -r -p IOService -l -w0 -c IOUSBHostDevice` output from an
// Apple-silicon Mac, captured against a LilyGO T-Display S3 (2026-09). The
// real thing taught three lessons the hand-written fixture had missed:
//
//   - The serial client sits under AppleUSBACMData under IOUSBHostInterface@1
//     under the IOUSBHostDevice — four levels down, with sibling interfaces
//     (the CDC control interface, a vendor-JTAG interface) that carry USB
//     identity properties but no serial client.
//   - macOS sanitizes product names: "USB Product Name" renders the ROM's
//     "USB JTAG/serial debug unit" as "USB JTAG_serial debug unit". The
//     un-sanitized "kUSBProductString" is the one worth preferring.
//   - User clients (apps that opened the device — "zoom.us", "sdrpp") attach
//     as AppleUSBHostDeviceUserClient children carrying no USB identity, and
//     AppleUSBCDCCompositeDevice sits directly under the device. None of
//     these must confuse the walk.
//
// The CH340-style device and the hub preserve the original fixture's cases:
// a device with no serial number, and a USB device with no serial client.
const ioregSample = `+-o USB JTAG/serial debug unit@08320000  <class IOUSBHostDevice, id 0x10001175f, registered, matched, active, busy 0 (214 ms), retain 40>
  | {
  |   "sessionID" = 2121326128428
  |   "idProduct" = 4097
  |   "iManufacturer" = 1
  |   "bDeviceClass" = 239
  |   "bcdDevice" = 257
  |   "iProduct" = 2
  |   "iSerialNumber" = 3
  |   "USB Product Name" = "USB JTAG_serial debug unit"
  |   "locationID" = 137494528
  |   "bcdUSB" = 512
  |   "kUSBSerialNumberString" = "E0:72:A1:A2:32:48"
  |   "USB Vendor Name" = "Espressif"
  |   "idVendor" = 12346
  |   "kUSBProductString" = "USB JTAG/serial debug unit"
  |   "USB Serial Number" = "E0:72:A1:A2:32:48"
  |   "kUSBVendorString" = "Espressif"
  | }
  |
  +-o AppleUSBCDCCompositeDevice  <class AppleUSBCDCCompositeDevice, id 0x100011762, !registered, !matched, active, busy 0, retain 4>
  |   {
  |     "IOClass" = "AppleUSBCDCCompositeDevice"
  |   }
  |
  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100011764, registered, matched, active, busy 0 (9 ms), retain 10>
  | | {
  | |   "idProduct" = 4097
  | |   "bInterfaceClass" = 2
  | |   "UsbExclusiveOwner" = "AppleUSBACMControl"
  | |   "locationID" = 137494528
  | |   "idVendor" = 12346
  | | }
  | |
  | +-o AppleUSBACMControl  <class AppleUSBACMControl, id 0x100011767, registered, matched, active, busy 0 (0 ms), retain 7>
  |     {
  |       "IOClass" = "AppleUSBACMControl"
  |     }
  |
  +-o IOUSBHostInterface@1  <class IOUSBHostInterface, id 0x100011765, registered, matched, active, busy 0 (208 ms), retain 8>
  | | {
  | |   "IOTTYBaseName" = "usbmodem"
  | |   "idProduct" = 4097
  | |   "bInterfaceClass" = 10
  | |   "locationID" = 137494528
  | |   "idVendor" = 12346
  | | }
  | |
  | +-o AppleUSBACMData  <class AppleUSBACMData, id 0x100011768, registered, matched, active, busy 0 (3 ms), retain 7>
  |   | {
  |   |   "IOClass" = "AppleUSBACMData"
  |   |   "IOTTYBaseName" = "usbmodem"
  |   |   "IOTTYSuffix" = "83201"
  |   | }
  |   |
  |   +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100011770, registered, matched, active, busy 0 (1 ms), retain 5>
  |       {
  |         "IOCalloutDevice" = "/dev/cu.usbmodem83201"
  |         "IODialinDevice" = "/dev/tty.usbmodem83201"
  |         "IOTTYSuffix" = "83201"
  |       }
  |
  +-o IOUSBHostInterface@2  <class IOUSBHostInterface, id 0x100011766, registered, matched, active, busy 0 (4 ms), retain 5>
  | | {
  | |   "bInterfaceSubClass" = 255
  | |   "bInterfaceClass" = 255
  | |   "idVendor" = 12346
  | |   "idProduct" = 4097
  | | }
  | |
  | +-o AppleUSBHostDeviceUserClient  <class AppleUSBHostDeviceUserClient, id 0x100011769, !registered, !matched, active, busy 0, retain 7>
  |       {
  |         "IOUserClientCreator" = "pid 671, zoom.us"
  |       }
  |
+-o USB Serial@01120000  <class IOUSBHostDevice, id 0x100000f01, registered, matched, active, busy 0 (362 ms), retain 49>
  | {
  |   "idProduct" = 29987
  |   "idVendor" = 6790
  |   "locationID" = 17956864
  |   "USB Product Name" = "USB Serial"
  | }
  |
  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100000f02, registered, matched, active, busy 0, retain 8>
  | | {
  | |   "idProduct" = 29987
  | |   "idVendor" = 6790
  | | }
  | |
  | +-o AppleUSBACMData  <class AppleUSBACMData, id 0x100000f03, registered, matched, active, busy 0, retain 7>
  |   | {
  |   |   "IOClass" = "AppleUSBACMData"
  |   |   "IOTTYBaseName" = "usbserial"
  |   |   "IOTTYSuffix" = "14220"
  |   | }
  |   |
  |   +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000f04, registered, matched, active, busy 0, retain 5>
  |       {
  |         "IOCalloutDevice" = "/dev/cu.usbserial-14220"
  |       }
  |
+-o USB2.0 Hub@01100000  <class IOUSBHostDevice, id 0x100000def, registered, matched, active, busy 0 (380 ms), retain 45>
  | {
  |   "idProduct" = 10531
  |   "idVendor" = 1155
  |   "locationID" = 17825792
  | }
  |
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
	d, ok := findByPath(got, "/dev/cu.usbmodem83201")
	if !ok {
		t.Fatalf("ESP32 not found in %+v", got)
	}
	// ioreg reports IDs in decimal: 12346 = 0x303a, 4097 = 0x1001.
	if d.VID != "303a" || d.PID != "1001" {
		t.Fatalf("got %s:%s, want 303a:1001 converted from decimal", d.VID, d.PID)
	}
	if d.Serial != "E0:72:A1:A2:32:48" {
		t.Fatalf("Serial = %q, want the unquoted serial number", d.Serial)
	}
	if d.PortPath != "0x08320000" {
		t.Fatalf("PortPath = %q, want the hex locationID 0x08320000", d.PortPath)
	}
	if d.Description != "USB JTAG/serial debug unit" {
		t.Fatalf("Description = %q, want the un-sanitized kUSBProductString", d.Description)
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

func TestParseIoregUserClientNoiseDoesNotBleedIdentity(t *testing.T) {
	// Apps that opened the device (zoom.us, sdrpp, Spotify) attach as
	// AppleUSBHostDeviceUserClient children. The parser must attribute the
	// serial client to the enclosing USB device without letting these
	// sibling subtrees corrupt it. The extraction test above covers the
	// happy path; this pins the negative: the walk stays stable in their
	// presence, and no phantom third device appears.
	got := parseSample(t)
	for _, d := range got {
		if d.VID == "" || d.PID == "" {
			t.Fatalf("device without USB identity leaked through: %+v", d)
		}
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
