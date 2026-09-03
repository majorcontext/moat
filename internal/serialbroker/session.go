package serialbroker

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/rfc2217"
	"github.com/majorcontext/moat/internal/serialport"
)

// listener owns one device's TCP listener and its at-most-one active session.
type listener struct {
	broker   *Broker
	runID    string
	approved Approved
	ln       net.Listener

	mu     sync.Mutex
	cur    *session
	closed bool
}

func (l *listener) serve() {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go l.handle(conn)
	}
}

// handle runs one connection. A device serves one connection at a time: two
// clients on one serial line would interleave bytes and corrupt both.
func (l *listener) handle(conn net.Conn) {
	s := &session{listener: l, conn: conn}

	l.mu.Lock()
	switch {
	case l.closed:
		l.mu.Unlock()
		conn.Close()
		return
	case l.cur != nil:
		l.mu.Unlock()
		log.Debug("serial connection refused; device busy",
			"device", l.approved.Name, "run", l.runID)
		l.emit("conflict", "a connection already holds this device", 0, 0)
		conn.Close()
		return
	}
	l.cur = s
	l.mu.Unlock()

	defer func() {
		l.mu.Lock()
		if l.cur == s {
			l.cur = nil
		}
		l.mu.Unlock()
		conn.Close()
	}()

	port, err := l.broker.openPort(l.approved.Device.Path)
	if err != nil {
		log.Debug("opening serial device failed", "device", l.approved.Name, "err", err)
		l.emit("error", err.Error(), 0, 0)
		return
	}
	s.setPort(port)
	defer port.Close()

	// Payload capture is opt-in: serial carries firmware images and device
	// credentials. A recorder that cannot be opened degrades to events only
	// rather than failing the session and stranding the hardware.
	if l.approved.Record == RecordFull && l.broker.openRecorder != nil {
		rec, rerr := l.broker.openRecorder(l.runID, l.approved.Name)
		if rerr != nil {
			log.Warn("serial payload capture unavailable", "device", l.approved.Name, "err", rerr)
			l.emit("error", "payload capture unavailable: "+rerr.Error(), 0, 0)
		} else {
			s.recorder = rec
			defer rec.Close()
		}
	}

	l.emit("attach", l.approved.Device.Path, 0, 0)

	s.run()

	l.emit("detach", "", s.tx.Load(), s.rx.Load())
}

// emit reports an event carrying this device's identity.
func (l *listener) emit(kind, detail string, tx, rx int64) {
	l.broker.emit(Event{
		RunID:        l.runID,
		Device:       l.approved.Name,
		Kind:         kind,
		Detail:       detail,
		DevicePath:   l.approved.Device.Path,
		VID:          l.approved.Device.VID,
		PID:          l.approved.Device.PID,
		DeviceSerial: l.approved.Device.Serial,
		Record:       l.approved.Record,
		TxBytes:      tx,
		RxBytes:      rx,
	})
}

func (l *listener) close() {
	l.mu.Lock()
	l.closed = true
	s := l.cur
	l.mu.Unlock()

	l.ln.Close()
	if s != nil {
		s.stop()
	}
}

// session pumps bytes between one container connection and one serial port,
// translating RFC2217 commands into port operations.
type session struct {
	listener *listener
	conn     net.Conn
	port     serialport.Port

	tx atomic.Int64 // bytes sent to the container
	rx atomic.Int64 // bytes received from the container

	mu       sync.Mutex
	settings serialport.Settings
	modem    serialport.Modem
	recorder io.WriteCloser
}

// record writes captured payload bytes, if capture is enabled.
func (s *session) record(dir string, b []byte) {
	s.mu.Lock()
	w := s.recorder
	s.mu.Unlock()
	if w == nil || len(b) == 0 {
		return
	}
	if _, err := fmt.Fprintf(w, "%s %s %x\n", time.Now().UTC().Format(time.RFC3339Nano), dir, b); err != nil {
		log.Debug("serial payload capture write failed", "device", s.listener.approved.Name, "err", err)
	}
}

// setPort publishes the open device to the session.
//
// It is guarded because Revoke and Close can tear a session down concurrently
// with the goroutine that opened it.
func (s *session) setPort(p serialport.Port) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.port = p
}

func (s *session) devicePort() serialport.Port {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// stop tears the session down, unblocking both pumps.
func (s *session) stop() {
	s.conn.Close()
	if p := s.devicePort(); p != nil {
		p.Close()
	}
}

func (s *session) run() {
	port := s.devicePort()
	var wg sync.WaitGroup
	wg.Add(2)

	// Container -> device.
	go func() {
		defer wg.Done()
		defer s.stop()
		r := rfc2217.NewReader(s.conn)
		r.OnCommand = s.handleCommand
		r.OnNegotiate = s.handleNegotiate
		n, err := io.Copy(&recordingWriter{w: port, dir: "tx", s: s}, r)
		s.rx.Add(n)
		s.logPumpExit("container->device", err)
	}()

	// Device -> container.
	go func() {
		defer wg.Done()
		defer s.stop()
		buf := make([]byte, 4096)
		var escaped []byte
		for {
			n, err := port.Read(buf)
			if n > 0 {
				s.record("rx", buf[:n])
				escaped = rfc2217.EscapeIAC(escaped[:0], buf[:n])
				if _, werr := s.conn.Write(escaped); werr != nil {
					s.logPumpExit("device->container", werr)
					return
				}
				s.tx.Add(int64(n))
			}
			if err != nil {
				s.logPumpExit("device->container", err)
				return
			}
		}
	}()

	wg.Wait()
}

// recordingWriter tees bytes headed for the device into the capture file.
type recordingWriter struct {
	w   io.Writer
	dir string
	s   *session
}

func (rw *recordingWriter) Write(p []byte) (int, error) {
	rw.s.record(rw.dir, p)
	return rw.w.Write(p)
}

// logPumpExit records why a pump stopped. A closed connection or port is the
// normal way a session ends, so those are not reported as errors.
func (s *session) logPumpExit(dir string, err error) {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return
	}
	log.Debug("serial pump stopped", "direction", dir,
		"device", s.listener.approved.Name, "err", err)
}

// handleNegotiate answers telnet option offers.
//
// A client that offers options and never hears back will stall before it sends
// any com-port command, so silence here looks like a hung device.
func (s *session) handleNegotiate(verb, option byte) {
	var reply byte
	switch verb {
	case rfc2217.Do:
		// The peer asks us to enable the option.
		if option == rfc2217.OptionBinary || option == rfc2217.OptionSGA || option == rfc2217.OptionComPort {
			reply = rfc2217.Will
		} else {
			reply = rfc2217.Wont
		}
	case rfc2217.Will:
		// The peer offers to enable it on their side.
		if option == rfc2217.OptionBinary || option == rfc2217.OptionSGA || option == rfc2217.OptionComPort {
			reply = rfc2217.Do
		} else {
			reply = rfc2217.Dont
		}
	default:
		// WONT/DONT need no answer; answering would loop.
		return
	}
	if err := rfc2217.WriteNegotiate(s.conn, reply, option); err != nil {
		log.Debug("telnet negotiation reply failed", "device", s.listener.approved.Name, "err", err)
	}
}

// handleCommand applies one com-port command to the device.
//
// Commands are applied synchronously on the read goroutine and never coalesced:
// driving an ESP32 into its bootloader is a specific sequence of DTR/RTS
// transitions, so reordering or merging them leaves the final state correct and
// the board unreset.
func (s *session) handleCommand(cmd byte, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch cmd {
	case rfc2217.CmdSetBaudRate:
		baud := rfc2217.DecodeBaud(payload)
		if baud == 0 {
			s.reply(cmd, rfc2217.EncodeBaud(s.settings.Baud))
			return
		}
		s.settings.Baud = baud
		s.applySettings()
		s.reply(cmd, rfc2217.EncodeBaud(s.settings.Baud))

	case rfc2217.CmdSetDataSize:
		if len(payload) == 1 && payload[0] >= 5 && payload[0] <= 8 {
			s.settings.DataBits = payload[0]
			s.applySettings()
		}
		s.reply(cmd, []byte{s.settings.DataBits})

	case rfc2217.CmdSetParity:
		if len(payload) == 1 {
			if p, ok := parityFromWire(payload[0]); ok {
				s.settings.Parity = p
				s.applySettings()
			}
		}
		s.reply(cmd, []byte{parityToWire(s.settings.Parity)})

	case rfc2217.CmdSetStopSize:
		if len(payload) == 1 && (payload[0] == 1 || payload[0] == 2) {
			s.settings.StopBits = payload[0]
			s.applySettings()
		}
		s.reply(cmd, []byte{s.settings.StopBits})

	case rfc2217.CmdSetControl:
		if len(payload) == 1 {
			s.applyControl(payload[0])
		}
		s.reply(cmd, payload)

	case rfc2217.CmdPurgeData:
		// pyserial issues PURGE from inside Serial.open() (reset_input_buffer
		// / reset_output_buffer are part of its connect sequence) and blocks
		// until the reply arrives, so this must be answered even though the
		// broker has nothing buffered. The reply echoes the request's value
		// byte: pyserial's check_answer rejects a mismatch.
		if len(payload) == 1 {
			switch payload[0] {
			case rfc2217.PurgeReceiveBuffer, rfc2217.PurgeTransmitBuffer, rfc2217.PurgeBothBuffers:
				if err := s.port.FlushBuffers(); err != nil {
					s.emitError("flushing device: " + err.Error())
					return
				}
				s.emit("purge", "buffers flushed")
			default:
				// Undefined purge value: acknowledge with the echo the client
				// expects rather than failing the session over it.
				s.emit("purge", fmt.Sprintf("unknown value %d", payload[0]))
			}
		}
		s.reply(cmd, payload)
	}
}

// applyControl handles SET-CONTROL, which carries both flow control and the
// individual modem lines.
func (s *session) applyControl(v byte) {
	switch v {
	case rfc2217.ControlFlowNone:
		s.settings.FlowControl = serialport.FlowNone
		s.applySettings()
	case rfc2217.ControlFlowXONXOFF:
		s.settings.FlowControl = serialport.FlowXONXOFF
		s.applySettings()
	case rfc2217.ControlFlowRTSCTS:
		s.settings.FlowControl = serialport.FlowRTSCTS
		s.applySettings()

	case rfc2217.ControlDTROn:
		s.modem.DTR = true
		s.applyModem(v)
	case rfc2217.ControlDTROff:
		s.modem.DTR = false
		s.applyModem(v)
	case rfc2217.ControlRTSOn:
		s.modem.RTS = true
		s.applyModem(v)
	case rfc2217.ControlRTSOff:
		s.modem.RTS = false
		s.applyModem(v)

	case rfc2217.ControlBreakOn:
		if err := s.port.SendBreak(); err != nil {
			s.emitError("sending break: " + err.Error())
			return
		}
		s.emit("break", rfc2217.ControlName(v))
	case rfc2217.ControlBreakOff:
		// Break is transmitted as a timed pulse by SendBreak, so the explicit
		// clear has nothing left to do.
	}
}

func (s *session) applySettings() {
	if err := s.port.ApplySettings(s.settings); err != nil {
		s.emitError("applying line settings: " + err.Error())
		return
	}
	s.emit("settings", formatSettings(s.settings))
}

func (s *session) applyModem(ctrl byte) {
	if err := s.port.SetModem(s.modem); err != nil {
		s.emitError("setting modem lines: " + err.Error())
		return
	}
	s.emit("modem", rfc2217.ControlName(ctrl))
}

// reply echoes a command back to the client, which is how RFC2217 confirms a
// setting was accepted. Clients block waiting for it.
func (s *session) reply(cmd byte, payload []byte) {
	if err := rfc2217.WriteCommand(s.conn, cmd+100, payload); err != nil {
		log.Debug("com-port reply failed", "device", s.listener.approved.Name, "err", err)
	}
}

func (s *session) emit(kind, detail string) {
	s.listener.emit(kind, detail, 0, 0)
}

func (s *session) emitError(detail string) {
	log.Debug("serial session error", "device", s.listener.approved.Name, "detail", detail)
	s.emit("error", detail)
}
