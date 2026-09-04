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
	if l.approved.Baud > 0 {
		// moat.yaml's `baud:` is the line rate the session opens at; a client
		// that sends its own SET-BAUDRATE later overrides it. config validates
		// baud as positive, and the practical range of UART rates is far below
		// 2^31, so the conversion cannot overflow.
		s.settings.Baud = uint32(l.approved.Baud) //nolint:gosec // G115: validated positive, bounded range
	}

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

	// Dead-peer detection without an idle timeout: a serial console can sit
	// quiet for hours by design, so inactivity must not close it. TCP
	// keepalives notice a peer that vanished without FIN (laptop lid, dead
	// network) and free the device for the next client instead of wedging the
	// one-session slot until the daemon restarts.
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(2 * time.Minute)
	}

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

	// The configured initial line rate goes down before the first byte
	// moves: a client that never sends SET-BAUDRATE (a plain reader) still
	// gets the rate moat.yaml asked for. A client that does send one
	// overrides it through the normal command path.
	if l.approved.Baud > 0 {
		s.applySettings()
	}

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
	record := l.approved.Record
	if record == "" {
		// Approved's documented default. Stamped here rather than trusting
		// every caller to normalize: an empty Record reads downstream as
		// "unknown" where the audit trail wants "events".
		record = "events"
	}
	l.broker.emit(Event{
		RunID:        l.runID,
		Device:       l.approved.Name,
		Kind:         kind,
		Detail:       detail,
		DevicePath:   l.approved.Device.Path,
		VID:          l.approved.Device.VID,
		PID:          l.approved.Device.PID,
		DeviceSerial: l.approved.Device.Serial,
		Record:       record,
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
	// pendingReplies holds payloads queued by reply() while s.mu is held;
	// handleCommand writes them after releasing the lock.
	pendingReplies [][]byte
	recorder       io.WriteCloser
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
		// A subnegotiation past the reader's cap is broken or hostile — the
		// largest real com-port command is 4 bytes of baud. Drop the
		// connection rather than serving whatever follows; the device is
		// released for the next client.
		r.SetOnBadCommand(func() {
			s.emitError("oversized com-port subnegotiation; connection dropped")
			s.stop()
		})
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
//
// The replies a command produces are written after the lock is released (see
// reply), never under it — a client that stops reading would otherwise stall
// every command handler and, because record() shares the mutex, the rx pump
// too, losing device bytes.
func (s *session) handleCommand(cmd byte, payload []byte) {
	s.mu.Lock()
	s.handleCommandLocked(cmd, payload)
	pending := s.pendingReplies
	s.pendingReplies = nil
	s.mu.Unlock()

	for _, r := range pending {
		s.writeReply(cmd, r)
	}
}

func (s *session) handleCommandLocked(cmd byte, payload []byte) {
	switch cmd {
	case rfc2217.CmdSetBaudRate:
		baud := rfc2217.DecodeBaud(payload)
		if baud == 0 {
			s.reply(rfc2217.EncodeBaud(s.settings.Baud))
			return
		}
		s.settings.Baud = baud
		s.applySettings()
		s.reply(rfc2217.EncodeBaud(s.settings.Baud))

	case rfc2217.CmdSetDataSize:
		if len(payload) == 1 && payload[0] >= 5 && payload[0] <= 8 {
			s.settings.DataBits = payload[0]
			s.applySettings()
		}
		s.reply([]byte{s.settings.DataBits})

	case rfc2217.CmdSetParity:
		if len(payload) == 1 {
			if p, ok := parityFromWire(payload[0]); ok {
				s.settings.Parity = p
				s.applySettings()
			}
		}
		s.reply([]byte{parityToWire(s.settings.Parity)})

	case rfc2217.CmdSetStopSize:
		if len(payload) == 1 && (payload[0] == 1 || payload[0] == 2) {
			s.settings.StopBits = payload[0]
			s.applySettings()
		}
		s.reply([]byte{s.settings.StopBits})

	case rfc2217.CmdSetControl:
		if len(payload) == 1 {
			if s.applyControl(payload[0]) {
				// A query queued its own answer (the current setting); the
				// echo below would send a second frame, and the client would
				// read it as the answer to its next command.
				break
			}
		}
		s.reply(payload)

	case rfc2217.CmdNotifyLineState:
		// The client asks the server to report line state (break/framing/error
		// bits). A tty in raw mode reports none of them — there is nothing to
		// say — but the reply must exist, or an asking client stalls. Zero is
		// the all-clear encoding.
		s.reply([]byte{0})

	case rfc2217.CmdNotifyModemState:
		// A poll: the client wants the modem status byte now. pyserial sends
		// this from .cts/.dsr/.ri/.cd reads when its `poll_modem` option is on
		// (rfc2217://...?poll_modem=1), and raises "remote sends no
		// NOTIFY_MODEMSTATE" if no answer ever arrives.
		s.replyModemState()

	case rfc2217.CmdSetLineStateMask, rfc2217.CmdSetModemStateMask:
		// The client subscribes to changes in the line/modem bits the payload
		// names. The broker does not push unsolicited notifications — change
		// pushes would need a watcher thread per session, and nothing moat
		// serves (esptool, miniterm) reads the lines that way; pyserial's
		// default is exactly this polling mode. Acknowledge the mask so the
		// client's wait completes; a client that then polls gets the answer
		// from the case above.
		s.reply(payload)

	case rfc2217.CmdPurgeData:
		// pyserial issues PURGE from inside Serial.open() (reset_input_buffer
		// / reset_output_buffer are part of its connect sequence) and blocks
		// until the reply arrives, so this must be answered even though the
		// broker has nothing buffered. The reply echoes the request's value
		// byte: pyserial's check_answer rejects a mismatch.
		//
		// A flush failure is reported but still acknowledged: the alternative
		// is a client that hangs on the ack and presents as an unreachable
		// device, when the truth is only that the buffers could not be
		// cleared. The data path still works; the client merely risks reading
		// a stale byte.
		if len(payload) == 1 {
			switch payload[0] {
			case rfc2217.PurgeReceiveBuffer, rfc2217.PurgeTransmitBuffer, rfc2217.PurgeBothBuffers:
				if err := s.port.FlushBuffers(); err != nil {
					s.emitError("flushing device: " + err.Error())
				} else {
					s.emit("purge", "buffers flushed")
				}
			default:
				// Undefined purge value: acknowledge with the echo the client
				// expects rather than failing the session over it.
				s.emit("purge", fmt.Sprintf("unknown value %d", payload[0]))
			}
		}
		s.reply(payload)
	}
}

// replyModemState queues a NOTIFY-MODEMSTATE answer carrying the port's input
// lines. Must be called with s.mu held (it reads s.port); the write happens in
// handleCommand's drain loop like every other reply.
func (s *session) replyModemState() {
	p := s.port
	if p == nil {
		return
	}
	st, err := p.ModemStatus()
	if err != nil {
		// The device cannot report its lines (some adapters do not wire
		// them). Answer with zero rather than silence: the client asked, and
		// a missing answer reads as a dead port.
		s.emitError("reading modem lines: " + err.Error())
		s.reply([]byte{0})
		return
	}
	var b byte
	if st.CTS {
		b |= rfc2217.ModemCTS
	}
	if st.DSR {
		b |= rfc2217.ModemDSR
	}
	if st.RI {
		b |= rfc2217.ModemRI
	}
	if st.CD {
		b |= rfc2217.ModemCD
	}
	s.reply([]byte{b})
}

// applyControl handles SET-CONTROL, which carries both flow control and the
// individual modem lines. It reports whether the value was a query whose
// answer it queued itself; the caller must not echo the request byte in that
// case, or the client reads the stray echo as the answer to its next command.
func (s *session) applyControl(v byte) (answered bool) {
	switch v {
	case rfc2217.ControlQueryFlow:
		// A query, not a change: answer with the current flow-control
		// setting rather than applying anything.
		var cur byte
		switch s.settings.FlowControl {
		case serialport.FlowRTSCTS:
			cur = rfc2217.ControlFlowRTSCTS
		case serialport.FlowXONXOFF:
			cur = rfc2217.ControlFlowXONXOFF
		default:
			cur = rfc2217.ControlFlowNone
		}
		s.reply([]byte{cur})
		return true
	case rfc2217.ControlQueryBreak:
		// Break is a timed pulse; it is off the moment the pulse ends.
		s.reply([]byte{rfc2217.ControlBreakOff})
		return true
	case rfc2217.ControlQueryDTR:
		if s.modem.DTR {
			s.reply([]byte{rfc2217.ControlDTROn})
		} else {
			s.reply([]byte{rfc2217.ControlDTROff})
		}
		return true
	case rfc2217.ControlQueryRTS:
		if s.modem.RTS {
			s.reply([]byte{rfc2217.ControlRTSOn})
		} else {
			s.reply([]byte{rfc2217.ControlRTSOff})
		}
		return true

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
			return false
		}
		s.emit("break", rfc2217.ControlName(v))
	case rfc2217.ControlBreakOff:
		// Break is transmitted as a timed pulse by SendBreak, so the explicit
		// clear has nothing left to do.
	default:
		// Inbound-flow and DCD/DTR/DSR flow-control variants. The broker
		// serves raw ttys, not modem banks; these settings have nothing to
		// act on, and SET-CONTROL's reply echoes the request byte anyway, so
		// the client is not left waiting. The event records what was asked.
		s.emit("control", rfc2217.ControlName(v))
	}
	return false
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

// reply queues the payload to echo back to the client, which is how RFC2217
// confirms a setting was accepted. Clients block waiting for it.
//
// The write happens outside s.mu: a stalled client's TCP window would hold
// the mutex across the write and stall every handler (and the rx pump, which
// needs the same mutex for record()) — UART bytes would be lost while the
// device is not drained.
func (s *session) reply(payload []byte) {
	s.pendingReplies = append(s.pendingReplies, payload)
}

// writeReply sends one queued reply. A deadline bounds the write so a client
// that stopped reading cannot hold the session's read goroutine forever; the
// connection is dropped on timeout — the device is released for the next
// client.
func (s *session) writeReply(cmd byte, payload []byte) {
	if tc, ok := s.conn.(*net.TCPConn); ok {
		_ = tc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	if err := rfc2217.WriteCommand(s.conn, cmd+100, payload); err != nil {
		log.Debug("com-port reply failed", "device", s.listener.approved.Name, "err", err)
		s.stop()
	}
	if tc, ok := s.conn.(*net.TCPConn); ok {
		_ = tc.SetWriteDeadline(time.Time{})
	}
}

func (s *session) emit(kind, detail string) {
	s.listener.emit(kind, detail, 0, 0)
}

func (s *session) emitError(detail string) {
	log.Debug("serial session error", "device", s.listener.approved.Name, "detail", detail)
	s.emit("error", detail)
}
