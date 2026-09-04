//go:build darwin

package serialdev

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

type ioregEnumerator struct{}

// NewEnumerator returns the host's serial device enumerator.
func NewEnumerator() Enumerator { return &ioregEnumerator{} }

// List implements Enumerator by shelling out to ioreg.
//
// macOS has no sysfs equivalent, and reading the identity attributes through
// IOKit directly would require cgo. ioreg is present on every macOS install.
//
// The plane choice is the whole game: the USB identity attributes
// (idVendor, idProduct, USB Serial Number, locationID) live on the
// IOUSBHostDevice, but the /dev node is created by the CDC driver stack
// (AppleUSBACMData → IOSerialBSDClient) which attaches only in the IOService
// plane. `ioreg -p IOUSB` shows the bus topology — every leaf an
// IOUSBHostDevice and nothing beneath it — so a device's serial client never
// appears there and every device would be dropped. `-r -p IOService
// -c IOUSBHostDevice` roots a separate subtree at each USB device and
// includes everything macOS attached below it; `-l` adds the properties.
// See parseIoreg for the parse.
func (e *ioregEnumerator) List(ctx context.Context) ([]Device, error) {
	out, err := e.ioreg(ctx)
	if err != nil {
		return nil, err
	}
	return parseIoreg(out)
}

// ListUSB implements USBEnumerator with the same ioreg invocation, keeping
// the USB devices parseIoreg drops: ones with an identity but no serial child.
func (e *ioregEnumerator) ListUSB(ctx context.Context) ([]Device, error) {
	out, err := e.ioreg(ctx)
	if err != nil {
		return nil, err
	}
	return parseIoregUSB(out)
}

func (e *ioregEnumerator) ioreg(ctx context.Context) (*bytes.Buffer, error) {
	cmd := exec.CommandContext(ctx, "ioreg", "-r", "-p", "IOService", "-l", "-w0", "-c", "IOUSBHostDevice")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("listing serial devices with ioreg: %w: %s", err, stderr.String())
	}
	return &stdout, nil
}
