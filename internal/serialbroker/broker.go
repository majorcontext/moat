// Package serialbroker serves host serial devices to containers over RFC2217.
//
// Each approved device gets its own listener. That is also the authentication
// model: RFC2217 carries no credentials, so reachability is the control — only
// the owning run's container is permitted to reach the port (the run manager
// adds it to the run's allowed host ports). Other processes on the host can
// still reach it; the docs state that boundary rather than implying a check
// that does not exist.
package serialbroker

import (
	"fmt"
	"net"
	"sync"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/serialport"
)

// Approved is a device a run may use.
type Approved struct {
	Name   string           // config name, e.g. "esp32"
	Device serialdev.Device // the resolved host device
	Record string           // "events" (default) or "full"
}

// Event describes something worth recording about a device session.
type Event struct {
	RunID   string
	Device  string
	Kind    string // attach, detach, error, settings, modem, break
	Detail  string
	TxBytes int64 // bytes sent to the container
	RxBytes int64 // bytes received from the container
}

// Options configures a Broker.
type Options struct {
	// OpenPort opens a host serial device. Defaults to serialport.Open; tests
	// substitute a fake so no hardware is required.
	OpenPort func(path string) (serialport.Port, error)

	// Log receives session events. Optional.
	Log func(Event)

	// BindAddr is the address listeners bind to. Defaults to all interfaces so
	// the container can reach it through the host gateway.
	BindAddr string
}

// Broker owns the listeners and device claims for every registered run.
type Broker struct {
	openPort func(string) (serialport.Port, error)
	logEvent func(Event)
	bindAddr string

	mu       sync.Mutex
	closed   bool
	byDevice map[string]*listener // device name -> listener
	byRun    map[string][]string  // run ID -> device names
}

// New creates a Broker.
func New(opts Options) *Broker {
	b := &Broker{
		openPort: opts.OpenPort,
		logEvent: opts.Log,
		bindAddr: opts.BindAddr,
		byDevice: map[string]*listener{},
		byRun:    map[string][]string{},
	}
	if b.openPort == nil {
		b.openPort = serialport.Open
	}
	if b.bindAddr == "" {
		b.bindAddr = "0.0.0.0"
	}
	return b
}

// Listen opens a listener for one approved device and returns its host:port.
//
// A device already claimed by another run is refused: two runs sharing a serial
// line would interleave bytes and corrupt both sessions.
func (b *Broker) Listen(runID string, a Approved) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return "", fmt.Errorf("serial broker is closed")
	}
	if existing, ok := b.byDevice[a.Name]; ok {
		return "", fmt.Errorf("serial device %q is already in use by run %s", a.Name, existing.runID)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(b.bindAddr, "0"))
	if err != nil {
		return "", fmt.Errorf("opening serial listener for %q: %w", a.Name, err)
	}

	l := &listener{
		broker:   b,
		runID:    runID,
		approved: a,
		ln:       ln,
	}
	b.byDevice[a.Name] = l
	b.byRun[runID] = append(b.byRun[runID], a.Name)

	go l.serve()

	log.Debug("serial listener started", "run", runID, "device", a.Name,
		"path", a.Device.Path, "addr", ln.Addr().String())
	return ln.Addr().String(), nil
}

// Revoke closes every listener and session belonging to a run. It is safe to
// call for a run that has no devices.
func (b *Broker) Revoke(runID string) {
	b.mu.Lock()
	names := b.byRun[runID]
	delete(b.byRun, runID)
	toClose := make([]*listener, 0, len(names))
	for _, name := range names {
		if l, ok := b.byDevice[name]; ok {
			delete(b.byDevice, name)
			toClose = append(toClose, l)
		}
	}
	b.mu.Unlock()

	for _, l := range toClose {
		l.close()
	}
}

// Close shuts every listener and session down.
func (b *Broker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	toClose := make([]*listener, 0, len(b.byDevice))
	for _, l := range b.byDevice {
		toClose = append(toClose, l)
	}
	b.byDevice = map[string]*listener{}
	b.byRun = map[string][]string{}
	b.mu.Unlock()

	for _, l := range toClose {
		l.close()
	}
	return nil
}

// emit reports an event, if a sink was configured.
func (b *Broker) emit(e Event) {
	if b.logEvent != nil {
		b.logEvent(e)
	}
}
