package rfc2217

import (
	"bytes"
	"io"
)

// Telnet control bytes.
const (
	iac = 0xFF // interpret as command
	se  = 0xF0 // subnegotiation end
	sb  = 0xFA // subnegotiation begin

	// Negotiation verbs, exported so peers can answer an offer.
	Will byte = 0xFB
	Wont byte = 0xFC
	Do   byte = 0xFD
	Dont byte = 0xFE
)

// Telnet options moat cares about. A serial stream must be in binary mode, or
// the peer is entitled to mangle high-bit bytes.
const (
	OptionBinary  byte = 0
	OptionEcho    byte = 1
	OptionSGA     byte = 3  // suppress go-ahead
	OptionComPort byte = 44 // com-port-control (RFC 2217)
)

// WriteNegotiate writes a telnet negotiation command, e.g. IAC WILL BINARY.
func WriteNegotiate(w io.Writer, verb, option byte) error {
	_, err := w.Write([]byte{iac, verb, option})
	return err
}

// EscapeIAC appends src to dst with every 0xFF doubled, as telnet requires.
//
// This matters more than it looks: 0xFF is the dominant byte in erased flash,
// so an unescaped stream corrupts firmware uploads in a way that presents as
// failing hardware rather than a protocol bug.
func EscapeIAC(dst, src []byte) []byte {
	for _, b := range src {
		if b == iac {
			dst = append(dst, iac, iac)
			continue
		}
		dst = append(dst, b)
	}
	return dst
}

// WriteCommand writes a com-port subnegotiation: IAC SB COM-PORT-OPTION cmd
// payload IAC SE, with the payload escaped.
func WriteCommand(w io.Writer, cmd byte, payload []byte) error {
	out := []byte{iac, sb, OptionComPort, cmd}
	out = EscapeIAC(out, payload)
	out = append(out, iac, se)
	_, err := w.Write(out)
	return err
}

// reader states.
const (
	stData = iota
	stIAC
	stSkipOption // consuming the option byte of DO/DONT/WILL/WONT
	stSubOption  // expecting the option byte of a subnegotiation
	stSubPayload
	stSubIAC // inside a subnegotiation, saw IAC
	stSync   // aborted subnegotiation, discarding until the next IAC
)

// MaxSubnegotiation is the largest subnegotiation payload the reader accepts.
//
// Real com-port subnegotiations carry at most a handful of bytes (baud is the
// largest at 4). A generous cap bounds the memory a hostile or broken peer can
// make the reader retain: the payload buffer grows with the stream until IAC
// SE arrives, and nothing else in the protocol stops it. A payload past the
// cap aborts the subnegotiation; the reader resynchronizes on the next frame.
const MaxSubnegotiation = 4096

// Reader un-escapes a telnet stream, yielding only the data bytes from Read and
// dispatching com-port subnegotiations to OnCommand.
//
// Commands for other telnet options are consumed and discarded so they never
// leak into the serial data stream.
type Reader struct {
	src io.Reader

	// OnCommand is called for each com-port subnegotiation, with the command
	// byte and its payload. The payload is only valid for the duration of the
	// call. Nil means commands are parsed and discarded.
	OnCommand func(cmd byte, payload []byte)

	// OnNegotiate is called for each IAC WILL/WONT/DO/DONT. A peer that offers
	// options and gets no answer will stall, so servers must respond to these.
	OnNegotiate func(verb, option byte)

	// onBadCommand, when set, is called when a subnegotiation is aborted for
	// exceeding MaxSubnegotiation. Servers can use it to drop the connection —
	// a peer that oversized a com-port command is broken or hostile, and the
	// data that follows the abort is not trustworthy.
	onBadCommand func()

	state   int
	verb    byte
	option  byte
	payload []byte
	out     bytes.Buffer
	buf     []byte
}

// SetOnBadCommand registers the oversized-subnegotiation callback. It returns
// the Reader so it can be chained off NewReader.
func (r *Reader) SetOnBadCommand(fn func()) *Reader {
	r.onBadCommand = fn
	return r
}

// NewReader wraps src.
func NewReader(src io.Reader) *Reader {
	return &Reader{src: src, buf: make([]byte, 4096)}
}

// Read implements io.Reader, returning decoded data bytes only.
func (r *Reader) Read(p []byte) (int, error) {
	for {
		if r.out.Len() > 0 {
			return r.out.Read(p)
		}
		n, err := r.src.Read(r.buf)
		if n > 0 {
			r.process(r.buf[:n])
		}
		if r.out.Len() > 0 {
			continue
		}
		if err != nil {
			return 0, err
		}
		// The chunk held nothing but command bytes; read more rather than
		// returning (0, nil). io.Reader's contract lets a caller treat (0, nil)
		// as a stall, and a source that actually returns it would busy-spin
		// this loop — so it is consumed here, not passed on.
	}
}

func (r *Reader) process(chunk []byte) {
	for _, b := range chunk {
		switch r.state {
		case stData:
			if b == iac {
				r.state = stIAC
				continue
			}
			r.out.WriteByte(b)

		case stIAC:
			switch b {
			case iac:
				// Doubled IAC is a literal 0xFF.
				r.out.WriteByte(iac)
				r.state = stData
			case sb:
				r.state = stSubOption
			case Do, Dont, Will, Wont:
				r.verb = b
				r.state = stSkipOption
			default:
				// Two-byte command with no argument (NOP, DM, ...).
				r.state = stData
			}

		case stSkipOption:
			if r.OnNegotiate != nil {
				r.OnNegotiate(r.verb, b)
			}
			r.state = stData

		case stSubOption:
			r.option = b
			r.payload = r.payload[:0]
			r.state = stSubPayload

		case stSubPayload:
			if b == iac {
				r.state = stSubIAC
				continue
			}
			if r.capExceeded() {
				continue
			}
			r.payload = append(r.payload, b)

		case stSubIAC:
			switch b {
			case iac:
				// A doubled IAC is one literal 0xFF of payload, so it counts
				// against the cap exactly like an ordinary byte — otherwise a
				// stream of FF FF pairs grows the buffer without bound, never
				// passing through the stSubPayload guard.
				if r.capExceeded() {
					continue
				}
				r.payload = append(r.payload, iac)
				r.state = stSubPayload
			case se:
				r.dispatch()
				r.state = stData
			default:
				// Malformed: a peer that sends IAC <other> mid-subnegotiation.
				// Drop the partial command rather than guessing at its meaning.
				r.state = stData
			}

		case stSync:
			// Aborted subnegotiation: discard bytes until the next IAC,
			// which either ends the runaway (IAC SE) or starts the next
			// command (IAC SB, IAC DO, ...).
			if b == iac {
				r.state = stIAC
			}
		}
	}
}

// capExceeded reports whether the subnegotiation payload has reached
// MaxSubnegotiation. When it has, it aborts the subnegotiation as a side
// effect: it releases the retained bytes, switches to stSync so the reader
// resynchronizes on the next IAC SB rather than swallowing the rest of the
// stream, and notifies onBadCommand so a server can drop a peer that oversized
// a com-port command (the largest real one is 4 bytes of baud).
func (r *Reader) capExceeded() bool {
	if len(r.payload) < MaxSubnegotiation {
		return false
	}
	r.state = stSync
	r.payload = r.payload[:0]
	if r.onBadCommand != nil {
		r.onBadCommand()
	}
	return true
}

func (r *Reader) dispatch() {
	if r.option != OptionComPort || len(r.payload) == 0 || r.OnCommand == nil {
		return
	}
	r.OnCommand(r.payload[0], r.payload[1:])
}
