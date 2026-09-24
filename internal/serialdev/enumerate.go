package serialdev

import "context"

// Enumerator lists serial devices attached to the host.
//
// It is an interface so tests can supply a fixed device list: every automated
// test in this area runs without hardware.
type Enumerator interface {
	List(ctx context.Context) ([]Device, error)
}

// USBEnumerator lists USB devices the serial broker cannot serve: ones with
// no serial interface at all (SDR dongles, keyboards, storage). It exists so
// `moat device list` can show a plugged-in-but-not-serial device instead of
// implying nothing is attached. Its devices carry a USB identity (VID/PID,
// serial, port path) but Path is always empty.
type USBEnumerator interface {
	ListUSB(ctx context.Context) ([]Device, error)
}

// EnumeratorFunc adapts a function to the Enumerator interface.
type EnumeratorFunc func(ctx context.Context) ([]Device, error)

// List implements Enumerator.
func (f EnumeratorFunc) List(ctx context.Context) ([]Device, error) { return f(ctx) }

// USBEnumeratorFunc adapts a function to the USBEnumerator interface.
type USBEnumeratorFunc func(ctx context.Context) ([]Device, error)

// ListUSB implements USBEnumerator.
func (f USBEnumeratorFunc) ListUSB(ctx context.Context) ([]Device, error) { return f(ctx) }
