package serialbroker

import (
	"fmt"

	"github.com/majorcontext/moat/internal/serialport"
)

// RFC2217 SET-PARITY values.
const (
	wireParityNone byte = 1
	wireParityOdd  byte = 2
	wireParityEven byte = 3
)

// parityFromWire converts an RFC2217 parity value. Mark and space parity are
// not represented: no serial tooling moat targets uses them, and silently
// treating them as "none" would corrupt framing.
func parityFromWire(v byte) (uint8, bool) {
	switch v {
	case wireParityNone:
		return serialport.ParityNone, true
	case wireParityOdd:
		return serialport.ParityOdd, true
	case wireParityEven:
		return serialport.ParityEven, true
	default:
		return serialport.ParityNone, false
	}
}

// parityToWire is the inverse, for confirmation replies.
func parityToWire(p uint8) byte {
	switch p {
	case serialport.ParityOdd:
		return wireParityOdd
	case serialport.ParityEven:
		return wireParityEven
	default:
		return wireParityNone
	}
}

// formatSettings renders line settings for logs and audit entries.
func formatSettings(s serialport.Settings) string {
	parity := "N"
	switch s.Parity {
	case serialport.ParityOdd:
		parity = "O"
	case serialport.ParityEven:
		parity = "E"
	}
	return fmt.Sprintf("%d %d%s%d", s.Baud, s.DataBits, parity, s.StopBits)
}
