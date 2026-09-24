// Package serialbroker serves host serial devices to containers over RFC2217.
//
// Each approved device gets its own listener. That is also the authentication
// model: RFC2217 carries no credentials, so reachability is the only control,
// and it is a narrowing rather than a boundary.
//
// The listener binds one container-facing host address instead of every
// interface. That is all it does. The run's allowed-host-ports entry is an
// egress permit applied inside the owning container under a strict network
// policy — it lets that container out, it does not keep anyone else away. Any
// process on the host, any other container that can route to the bound
// address, and on Linux any sender whose packet reaches a local address on any
// interface can connect. What prevents two clients driving one device is the
// exclusive claim plus one session at a time, not reachability.
package serialbroker

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/serialport"
)

// Approved is a device a run may use.
type Approved struct {
	Name   string           // config name, e.g. "esp32"
	Device serialdev.Device // the resolved host device
	Record string           // "events" (default) or "full"
	// Baud is the initial line rate from moat.yaml's `baud:`. Zero means the
	// client's SET-BAUDRATE governs from the first command.
	Baud int
}

// Event describes something worth recording about a device session.
//
// The device's USB identity travels with every event so an audit trail records
// which physical device was reached, not just the name the config gave it.
type Event struct {
	RunID  string
	Device string // config name, e.g. "esp32"
	Kind   string // attach, detach, error, conflict, settings, modem, control, break, purge
	Detail string

	DevicePath   string // host device node
	VID          string
	PID          string
	DeviceSerial string // the device's USB serial number, if it has one
	Record       string // record mode in force for this session

	TxBytes int64 // bytes sent to the container
	RxBytes int64 // bytes received from the container
}

// RecordFull is the Approved.Record value that enables payload capture.
const RecordFull = "full"

// Options configures a Broker.
type Options struct {
	// OpenPort opens a host serial device. Defaults to serialport.Open; tests
	// substitute a fake so no hardware is required.
	OpenPort func(path string) (serialport.Port, error)

	// Log receives session events. Optional.
	Log func(Event)

	// OpenRecorder returns a sink for captured payload bytes, used only when a
	// device is configured with record: full. Optional; without it, full
	// capture degrades to events only rather than failing the session.
	OpenRecorder func(runID, device string) (io.WriteCloser, error)

	// BindAddr is the fallback address for listeners when ListenOpts carries
	// none. Defaults to loopback — the only address safe to expose without
	// authentication. The run manager knows the container's gateway and passes
	// it per device; a broker configured for all interfaces is a deployment
	// choice that must be made explicitly, not a default.
	BindAddr string

	// HandshakeTimeout bounds how long an accepted connection may hold the
	// device without completing a negotiation, a com-port command, or sending
	// payload. Zero uses the default. See handshakeTimeout in session.go for
	// why this is an absolute budget rather than an idle timeout.
	HandshakeTimeout time.Duration
}

// Broker owns the listeners and device claims for every registered run.
type Broker struct {
	openPort     func(string) (serialport.Port, error)
	logEvent     func(Event)
	openRecorder func(runID, device string) (io.WriteCloser, error)
	bindAddr     string
	handshakeTO  time.Duration

	mu       sync.Mutex
	closed   bool
	byDevice map[string]*listener // claim key -> listener
	byRun    map[string][]string  // run ID -> claim keys
}

// claimKey identifies the physical device a claim holds, independent of the
// config name it was approved under. Two config names resolving to the same
// device node must share a claim — otherwise both open the port and interleave
// bytes on the same line, which is exactly what the claim exists to prevent.
// The device path is used directly because it is what the session re-opens;
// VID/PID/serial would be equivalent at run start (resolution happened against
// them) but the path is the stronger key once a device is unplugged and its
// node reused.
func claimKey(a Approved) string {
	if a.Device.Path != "" {
		return "path:" + a.Device.Path
	}
	// A device with no path (never seen in practice — resolution requires a
	// node) still claims on its identity so it cannot be double-listened.
	id := a.Device.Identity()
	return "id:" + id.VID + ":" + id.PID + ":" + id.Serial + ":" + id.PortPath
}

// New creates a Broker.
func New(opts Options) *Broker {
	b := &Broker{
		openPort:     opts.OpenPort,
		logEvent:     opts.Log,
		openRecorder: opts.OpenRecorder,
		bindAddr:     opts.BindAddr,
		handshakeTO:  opts.HandshakeTimeout,
		byDevice:     map[string]*listener{},
		byRun:        map[string][]string{},
	}
	if b.openPort == nil {
		b.openPort = serialport.Open
	}
	if b.bindAddr == "" {
		b.bindAddr = "127.0.0.1"
	}
	return b
}

// Listen opens a listener for one approved device and returns a handle for
// scoped rollback plus its host:port address. The port is OS-assigned.
//
// A device already claimed by another run is refused: two runs sharing a serial
// line would interleave bytes and corrupt both sessions.
//
// Re-listening a device this run already holds is idempotent and returns the
// existing address: the run manager re-registers with the daemon after a
// transient daemon failure, and a run must not conflict with its own claim.
//
// bindAddr overrides the broker's fallback BindAddr for this listener. RFC2217
// carries no authentication, so the address is the reachability control: the
// run manager passes the container-facing address of the run's network, and an
// empty value means the broker's configured default.
func (b *Broker) Listen(runID string, a Approved, bindAddr string) (*ListenerRef, string, error) {
	return b.listen(runID, a, bindAddr, 0)
}

// ListenAt is Listen bound to one exact port. The container's
// MOAT_SERIAL_*_URL froze the port at container create, so restoring a run
// after a daemon restart must re-bind the same number — rebinding elsewhere
// would leave the run's device silently dead while the daemon looks healthy.
// A taken port fails with a named error rather than falling back to an
// OS-assigned one.
func (b *Broker) ListenAt(runID string, a Approved, bindAddr string, port int) (*ListenerRef, string, error) {
	return b.listen(runID, a, bindAddr, port)
}

// listen is the shared core of Listen and ListenAt.
func (b *Broker) listen(runID string, a Approved, bindAddr string, port int) (*ListenerRef, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, "", fmt.Errorf("serial broker is closed")
	}
	if bindAddr == "" {
		bindAddr = b.bindAddr
	}
	key := claimKey(a)
	if existing, ok := b.byDevice[key]; ok {
		if existing.runID == runID {
			// The run already holds this claim; hand back a ref marked
			// preexisting so a scoped rollback of *this* call does not close a
			// listener it did not open.
			return &ListenerRef{broker: b, l: existing, preexisting: true}, existing.ln.Addr().String(), nil
		}
		return nil, "", fmt.Errorf("serial device %q (%s) is already in use by run %s",
			a.Name, a.Device.Path, existing.runID)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(bindAddr, strconv.Itoa(port)))
	if err != nil {
		return nil, "", fmt.Errorf("opening serial listener for %q: %w", a.Name, err)
	}

	l := &listener{
		broker:   b,
		runID:    runID,
		approved: a,
		ln:       ln,
	}
	b.byDevice[key] = l
	b.byRun[runID] = append(b.byRun[runID], key)

	go l.serve()

	log.Debug("serial listener started", "run", runID, "device", a.Name,
		"path", a.Device.Path, "addr", ln.Addr().String())
	return &ListenerRef{broker: b, l: l}, ln.Addr().String(), nil
}

// ListenerRef names one listener a specific Listen call opened, so a caller
// can roll back exactly that set without disturbing the run's other devices.
type ListenerRef struct {
	broker *Broker
	l      *listener
	// preexisting marks a ref returned for a claim the run already held (an
	// idempotent re-Listen). CloseListeners skips it: rolling back the call
	// that received it must not tear down a listener it did not open.
	preexisting bool
}

// CloseListeners releases exactly the listeners the given refs point at.
// listenSerial uses it to roll back a partially-failed registration without
// touching the live listeners of the same run: re-registration after a
// transient daemon failure must not tear down devices a running container is
// using. A ref whose listener was already released (or re-created by a later
// registration) is a no-op for it — the registry entry is only removed when it
// still points at that listener.
func (b *Broker) CloseListeners(refs []*ListenerRef) {
	if len(refs) == 0 {
		return
	}
	ls := make([]*listener, 0, len(refs))
	seen := map[*listener]bool{}
	for _, r := range refs {
		if r != nil && r.broker == b && !r.preexisting && !seen[r.l] {
			seen[r.l] = true
			ls = append(ls, r.l)
		}
	}
	if len(ls) == 0 {
		return
	}
	b.mu.Lock()
	for _, l := range ls {
		key := claimKey(l.approved)
		if cur, ok := b.byDevice[key]; ok && cur == l {
			delete(b.byDevice, key)
		}
		b.byRun[l.runID] = removeString(b.byRun[l.runID], key)
	}
	b.mu.Unlock()

	for _, l := range ls {
		l.close()
	}
}

// Revoke closes every listener and session belonging to a run. It is safe to
// call for a run that has no devices.
func (b *Broker) Revoke(runID string) {
	b.mu.Lock()
	keys := b.byRun[runID]
	delete(b.byRun, runID)
	toClose := make([]*listener, 0, len(keys))
	for _, key := range keys {
		if l, ok := b.byDevice[key]; ok {
			delete(b.byDevice, key)
			toClose = append(toClose, l)
		}
	}
	b.mu.Unlock()

	for _, l := range toClose {
		l.close()
	}
}

func removeString(ss []string, s string) []string {
	for i, v := range ss {
		if v == s {
			return append(ss[:i], ss[i+1:]...)
		}
	}
	return ss
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
