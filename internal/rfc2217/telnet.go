package rfc2217

import (
	"bytes"
	"io"
)

// Telnet control bytes.
const (
	iac  = 0xFF // interpret as command
	se   = 0xF0 // subnegotiation end
	sb   = 0xFA // subnegotiation begin
	will = 0xFB
	wont = 0xFC
	do   = 0xFD
	dont = 0xFE

	// OptionComPort is the client-to-server com-port-control option.
	OptionComPort = 44
)

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
)

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

	state   int
	option  byte
	payload []byte
	out     bytes.Buffer
	buf     []byte
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
		// returning (0, nil), which callers are entitled to treat as a stall.
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
			case do, dont, will, wont:
				r.state = stSkipOption
			default:
				// Two-byte command with no argument (NOP, DM, ...).
				r.state = stData
			}

		case stSkipOption:
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
			r.payload = append(r.payload, b)

		case stSubIAC:
			switch b {
			case iac:
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
		}
	}
}

func (r *Reader) dispatch() {
	if r.option != OptionComPort || len(r.payload) == 0 || r.OnCommand == nil {
		return
	}
	r.OnCommand(r.payload[0], r.payload[1:])
}
