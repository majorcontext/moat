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
  |   "bDeviceClass" = 9
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

// The hub in the fixture above is the negative case for the serial walk; here
// a device with a real identity and no serial child pins the USB walk: an
// SDR dongle, which is the device that made the gap visible.
const ioregSDRSample = `+-o RTL2832U@02100000  <class IOUSBHostDevice, id 0x100022aaf, registered, matched, active, busy 0 (123 ms), retain 9>
  | {
  |   "idProduct" = 10296
  |   "idVendor" = 3034
  |   "USB Serial Number" = "00000001"
  |   "locationID" = 34603008
  |   "USB Product Name" = "RTL2832U"
  | }
  |
  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100022ab0, registered, matched, active, busy 0 (5 ms), retain 5>
  | | {
  | |   "bInterfaceClass" = 255
  | |   "idProduct" = 10296
  | |   "idVendor" = 3034
  | | }
  | |
  | +-o IOUSBHostDeviceUserClient  <class IOUSBHostDeviceUserClient, id 0x100022ab1, !registered, !matched, active, busy 0, retain 5>
  |       {
  |         "IOUserClientCreator" = "pid 301, sdrpp"
  |       }
  |
`

func TestParseIoregUSBFindsNonSerialDevices(t *testing.T) {
	got, err := parseIoregUSB(strings.NewReader(ioregSDRSample))
	if err != nil {
		t.Fatalf("parseIoregUSB: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(got), got)
	}
	d := got[0]
	if d.Path != "" {
		t.Fatalf("Path = %q, want empty — this device has no tty", d.Path)
	}
	// ioreg reports IDs in decimal: 3034 = 0x0bda, 10296 = 0x2838.
	if d.VID != "0bda" || d.PID != "2838" {
		t.Fatalf("got %s:%s, want 0bda:2838 (RTL2832U)", d.VID, d.PID)
	}
	if d.Serial != "00000001" {
		t.Fatalf("Serial = %q, want 00000001", d.Serial)
	}
	if d.PortPath != "0x02100000" {
		t.Fatalf("PortPath = %q, want 0x02100000", d.PortPath)
	}
	if d.Description != "RTL2832U" {
		t.Fatalf("Description = %q, want RTL2832U", d.Description)
	}
}

func TestParseIoregUSBSkipsSerialDevices(t *testing.T) {
	// Companion: the main fixture's two serial devices must not appear in the
	// USB list — they are the serial walk's output, and showing them here
	// would double-report every board.
	got, err := parseIoregUSB(strings.NewReader(ioregSample))
	if err != nil {
		t.Fatalf("parseIoregUSB: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none — both fixture devices are serial", got)
	}
}

func TestParseIoregAndUSBPartitionTheSample(t *testing.T) {
	// The two walks together must cover every identifiable device exactly
	// once: serial devices in List, everything else in ListUSB.
	serial, err := parseIoreg(strings.NewReader(ioregSample))
	if err != nil {
		t.Fatalf("parseIoreg: %v", err)
	}
	usb, err := parseIoregUSB(strings.NewReader(ioregSample))
	if err != nil {
		t.Fatalf("parseIoregUSB: %v", err)
	}
	// ioregSample has 2 serial devices (ESP32, CH340); the hub has no
	// idVendor/idProduct pair and is dropped by both walks.
	if len(serial) != 2 || len(usb) != 0 {
		t.Fatalf("serial = %d, usb = %d, want 2 and 0", len(serial), len(usb))
	}

	// Mixed: the SDR fixture added on top, both walks stay disjoint.
	combined := ioregSample + ioregSDRSample
	serial, err = parseIoreg(strings.NewReader(combined))
	if err != nil {
		t.Fatalf("parseIoreg: %v", err)
	}
	usb, err = parseIoregUSB(strings.NewReader(combined))
	if err != nil {
		t.Fatalf("parseIoregUSB: %v", err)
	}
	if len(serial) != 2 || len(usb) != 1 {
		t.Fatalf("serial = %d, usb = %d, want 2 and 1 (the SDR only in USB)", len(serial), len(usb))
	}
}

// Real-hardware regression: Macs carry internal hub controllers (e.g.
// Microchip 0424:7240 "USB2 Controller Hub") whose ioreg blocks report no
// class-9 bDeviceClass. The class filter alone let them into the USB section
// of `moat device list` on a real machine. The product name is the backstop.
func TestParseIoregUSBSkipsControllerHubsWithNoDeviceClass(t *testing.T) {
	const sample = `+-o USB2 Controller Hub@02300000  <class IOUSBHostDevice, id 0x100022bc0, registered, matched, active, busy 0 (210 ms), retain 16>
  | {
  |   "idProduct" = 29248
  |   "idVendor" = 1060
  |   "locationID" = 36765696
  |   "USB Product Name" = "USB2 Controller Hub"
  | }
  |
  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100022bc1, registered, matched, active, busy 0 (3 ms), retain 5>
  | {
  |   "IOUserClientCreator" = "pid 154, WindowServer"
  | }
  |
`
	got, err := parseIoregUSB(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parseIoregUSB: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("internal controller hub must be skipped, got %+v", got)
	}

	// Companion: a class-9 device whose product string does NOT say "hub"
	// (a downstream hub on a real bus) is still dropped by the class filter.
	const classOnly = `+-o Bus-Powered Device@02310000  <class IOUSBHostDevice, id 0x100022bc2, registered, matched, active, busy 0 (5 ms), retain 9>
  | {
  |   "idProduct" = 1
  |   "idVendor" = 2
  |   "bDeviceClass" = 9
  |   "locationID" = 36765696
  |   "USB Product Name" = "Bus-Powered Device"
  | }
  |
`
	got, err = parseIoregUSB(strings.NewReader(classOnly))
	if err != nil {
		t.Fatalf("parseIoregUSB: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("class-9 device must be skipped regardless of name, got %+v", got)
	}
}

// ioregDualUART is the ioreg shape of a dual-UART bridge (FT2232H): one
// IOUSBHostDevice, two IOUSBHostInterfaces, one serial client each. Written
// from the structure of the existing captured samples (decimal IDs, tree
// prefixes, IOSerialBSDClient property blocks).
const ioregDualUART = `
+-o USB Dual UART@14100000  <class IOUSBHostDevice, id 0x100000f01, registered, matched, active, busy 0 (362 ms), retain 49>
  | {
  |   "idProduct" = 24577
  |   "idVendor" = 1027
  |   "USB Serial Number" = "FT7ABCDE"
  |   "locationID" = 33667584
  |   "kUSBProductString" = "USB Dual UART"
  | }
  |
  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100000f02, registered, matched, active, busy 0, retain 8>
  | | {
  | |   "idProduct" = 24577
  | |   "idVendor" = 1027
  | | }
  | |
  | +-o AppleUSBACMData  <class AppleUSBACMData, id 0x100000f03, registered, matched, active, busy 0, retain 7>
  |   | {
  |   |   "IOTTYBaseName" = "usbserial"
  |   |   "IOTTYSuffix" = "FT7ABCD0"
  |   | }
  |   |
  |   +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000f04, registered, matched, active, busy 0, retain 5>
  |       {
  |         "IOCalloutDevice" = "/dev/cu.usbserial-FT7ABCD0"
  |       }
  |
  +-o IOUSBHostInterface@1  <class IOUSBHostInterface, id 0x100000f05, registered, matched, active, busy 0, retain 8>
  | | {
  | |   "idProduct" = 24577
  | |   "idVendor" = 1027
  | | }
  | |
  | +-o AppleUSBACMData  <class AppleUSBACMData, id 0x100000f06, registered, matched, active, busy 0, retain 7>
  |   | {
  |   |   "IOTTYBaseName" = "usbserial"
  |   |   "IOTTYSuffix" = "FT7ABCD1"
  |   | }
  |   |
  |   +-o IOSerialBSDClient  <class IOSerialBSDClient, id 0x100000f07, registered, matched, active, busy 0, retain 5>
  |       {
  |         "IOCalloutDevice" = "/dev/cu.usbserial-FT7ABCD1"
  |       }
  |
`

func TestParseIoregSeparatesDualUARTPorts(t *testing.T) {
	// One plug, two ttys (FT2232H). Without one Device per serial client the
	// clients overwrite each other: the sample parsed to a single device and
	// port A was unreachable.
	got, err := parseIoreg(strings.NewReader(ioregDualUART))
	if err != nil {
		t.Fatalf("parseIoreg: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d devices, want one per UART: %+v", len(got), got)
	}
	a, ok := findByPath(got, "/dev/cu.usbserial-FT7ABCD0")
	if !ok {
		t.Fatalf("port A not found in %+v", got)
	}
	b, ok := findByPath(got, "/dev/cu.usbserial-FT7ABCD1")
	if !ok {
		t.Fatalf("port B not found in %+v", got)
	}
	if a.Interface != "0" || b.Interface != "1" {
		t.Fatalf("interfaces = %q/%q, want 0/1 — the only discriminator between the UARTs", a.Interface, b.Interface)
	}
	if a.Identity() == b.Identity() {
		t.Fatalf("both UARTs share identity %+v; they cannot pin separately", a.Identity())
	}
	// The shared identity fields stay shared: both are the same bridge.
	if a.VID != b.VID || a.PID != b.PID || a.Serial != b.Serial || a.PortPath != b.PortPath {
		t.Fatalf("ports diverged beyond the interface: %+v vs %+v", a, b)
	}
}

func TestParseIoregSingleUARTDeviceDoesNotSplit(t *testing.T) {
	// Companion: the dual-UART split must not multiply single-UART hardware.
	// The sample's modem has three interfaces but one serial client, so it
	// must parse to exactly one Device.
	got := parseSample(t)
	byPath := map[string]int{}
	for _, d := range got {
		byPath[d.Path]++
	}
	for path, n := range byPath {
		if n != 1 {
			t.Fatalf("device %s appeared %d times; one serial client must yield one Device", path, n)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d devices, want the sample's 2: %+v", len(got), got)
	}
}

// ioregNestedPortLocation is the ioreg shape that corrupted pins before the
// own-property-block rule: a hub whose IOUSBHostDevice block is followed by
// children (hub port nubs, an interface) carrying their own, different
// locationID. The nubs sit below the device line, so a last-write-wins parser
// attributes the child's locationID to the hub.
const ioregNestedPortLocation = `
+-o USB2 Hub@08300000  <class IOUSBHostDevice, id 0x100000def, registered, matched, active, busy 0 (380 ms), retain 45>
  | {
  |   "idProduct" = 10531
  |   "idVendor" = 1155
  |   "bDeviceClass" = 9
  |   "locationID" = 137363456
  | }
  |
  +-o IOUSBHostInterface@0  <class IOUSBHostInterface, id 0x100000e01, registered, matched, active, busy 0, retain 8>
  | | {
  | |   "idProduct" = 10531
  | |   "idVendor" = 1155
  | |   "locationID" = 137494528
  | | }
  | |
  +-o Hub Port 2@08320000  <class AppleUSB20HubPort, id 0x100000e02, registered, matched, active, busy 0, retain 5>
  |   {
  |     "locationID" = 137494528
  |     "idVendor" = 6790
  |     "idProduct" = 29987
  |   }
  |
`

func TestParseIoregNestedChildrenDoNotOverwriteTheDevice(t *testing.T) {
	// A nested child's locationID must not become the device's PortPath —
	// it is the identity fallback for serial-less devices, so a corrupted
	// value corrupts the pin. On the real dump that produced this shape, a
	// hub parsed with a downstream port's locationID, masked only because
	// the interface's block happened to print last.
	all, err := parseIoregAll(strings.NewReader(ioregNestedPortLocation))
	if err != nil {
		t.Fatalf("parseIoregAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d devices, want the hub alone: %+v", len(all), all)
	}
	if all[0].PortPath != "0x08300000" {
		t.Fatalf("PortPath = %q, want the device's own locationID 0x08300000, not a child's", all[0].PortPath)
	}
	// The child's IDs must not leak either: the hub is 0483:2923 (decimal
	// 1155:10531), the port nub below it 1a86:7523 (decimal 6790:29987).
	if all[0].VID != "0483" {
		t.Fatalf("VID = %q, want the hub's own 0483, not the child's 1a86", all[0].VID)
	}
}

// ioregNoIdentity is an IOUSBHostDevice with a USB ID but neither a serial
// number nor a locationID — no way to pin it. Such blocks come off the wire
// when ioreg prints nothing after `=` for unparseable values.
const ioregNoIdentity = `
+-o Mystery Device@01100000  <class IOUSBHostDevice, id 0x100000f11, registered, matched, active, busy 0 (0 ms), retain 5>
  | {
  |   "idProduct" = 29987
  |   "idVendor" = 6790
  | }
  |
`

func TestParseIoregDropsDevicesWithNoIdentity(t *testing.T) {
	// A device with neither serial number nor locationID cannot be pinned —
	// a pin for it would approve any device with the same USB ID. It must be
	// dropped from both lists rather than offered.
	serial, err := parseIoreg(strings.NewReader(ioregNoIdentity))
	if err != nil {
		t.Fatalf("parseIoreg: %v", err)
	}
	usb, err := parseIoregUSB(strings.NewReader(ioregNoIdentity))
	if err != nil {
		t.Fatalf("parseIoregUSB: %v", err)
	}
	if len(serial) != 0 || len(usb) != 0 {
		t.Fatalf("serial = %+v, usb = %+v, want both empty — the device cannot be pinned", serial, usb)
	}
}

func TestParseIoregKeepsSerialLessDevicesWithALocation(t *testing.T) {
	// Companion: the serial-less CH340 in the sample keeps its place — its
	// locationID is its whole identity, and dropping it would make a working
	// pinned device unreachable.
	got := parseSample(t)
	d, ok := findByPath(got, "/dev/cu.usbserial-14220")
	if !ok {
		t.Fatalf("CH340 not found in %+v", got)
	}
	if d.Serial == "" && d.PortPath == "" {
		t.Fatalf("the CH340 must keep its locationID as identity")
	}
}

func TestParseIoregKeepsDescriptionWhenProductStringIsBlank(t *testing.T) {
	// ioreg prints nothing after `=` for non-ASCII strings (a RØDE NT-USB
	// Mini on this host listed with a blank description because of it). The
	// empty kUSBProductString must not clobber the sanitized USB Product
	// Name that came before.
	const sample = `
+-o RODE NT-USB Mini@01100000  <class IOUSBHostDevice, id 0x100000f11, registered, matched, active, busy 0 (0 ms), retain 5>
  | {
  |   "idProduct" = 29987
  |   "idVendor" = 6790
  |   "locationID" = 17825792
  |   "USB Product Name" = "NT-USB Mini"
  |   "kUSBProductString" = 
  | }
  |
`
	all, err := parseIoregAll(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parseIoregAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(all), all)
	}
	if all[0].Description != "NT-USB Mini" {
		t.Fatalf("Description = %q, want the sanitized name — the blank kUSBProductString must not clobber it", all[0].Description)
	}
}

func TestDecimalToHexIDRejectsOutOfRangeValues(t *testing.T) {
	// A decimal above 0xffff would format as 5+ hex digits that can never
	// match moat.yaml's 4-digit form; treat it as unparseable so the device
	// is dropped for a statable reason.
	if got := decimalToHexID("70000"); got != "" {
		t.Fatalf("decimalToHexID(70000) = %q, want empty", got)
	}
	if got := decimalToHexID("65535"); got != "ffff" {
		t.Fatalf("decimalToHexID(65535) = %q, want ffff — the top of the range must still convert", got)
	}
	if got := decimalToHexID("12346"); got != "303a" {
		t.Fatalf("decimalToHexID(12346) = %q, want 303a", got)
	}
}
