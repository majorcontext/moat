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
// IOKit directly would require cgo. ioreg is present on every macOS install and
// its tree output carries everything needed: see parseIoreg.
func (e *ioregEnumerator) List(ctx context.Context) ([]Device, error) {
	cmd := exec.CommandContext(ctx, "ioreg", "-p", "IOUSB", "-l", "-w0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("listing serial devices with ioreg: %w: %s", err, stderr.String())
	}
	return parseIoreg(&stdout)
}
