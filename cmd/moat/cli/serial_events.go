package cli

import (
	"time"

	"github.com/majorcontext/moat/internal/audit"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/serialbroker"
	"github.com/majorcontext/moat/internal/storage"
)

// serialEventFanout routes broker events to their two sinks: every event goes
// to the run's devices.jsonl, and session boundaries (attach, detach, error,
// conflict) also go to the audit chain — attaching to hardware an agent can
// reflash is worth a tamper-evident record, while per-signal chatter is not.
//
// The per-run stores are supplied by the daemon rather than opened here, so a
// run has exactly one *storage.RunStore and one *audit.Store shared with the
// network and policy loggers. A second audit.Store on the same audit.db would
// collide on the chain's seq primary key and silently drop device entries.
type serialEventFanout struct {
	runStore   func(runID string) *storage.RunStore
	auditStore func(runID string) *audit.Store
}

func newSerialEventFanout(
	runStore func(runID string) *storage.RunStore,
	auditStore func(runID string) *audit.Store,
) *serialEventFanout {
	return &serialEventFanout{runStore: runStore, auditStore: auditStore}
}

// handle is the serialbroker.Options.Log callback.
func (f *serialEventFanout) handle(e serialbroker.Event) {
	log.Debug("serial device event", "run", e.RunID, "device", e.Device,
		"kind", e.Kind, "detail", e.Detail, "tx", e.TxBytes, "rx", e.RxBytes)
	if e.RunID == "" {
		return
	}

	if store := f.runStore(e.RunID); store != nil {
		_ = store.WriteDeviceEvent(storage.DeviceEvent{
			Timestamp: time.Now().UTC(),
			Device:    e.Device,
			Kind:      e.Kind,
			Detail:    e.Detail,
			TxBytes:   e.TxBytes,
			RxBytes:   e.RxBytes,
		})
	}

	switch e.Kind {
	case "attach", "detach", "error", "conflict":
	default:
		return
	}

	if as := f.auditStore(e.RunID); as != nil {
		_, _ = as.AppendDevice(audit.DeviceData{
			Name:       e.Device,
			Path:       e.DevicePath,
			VID:        e.VID,
			PID:        e.PID,
			Serial:     e.DeviceSerial,
			Action:     e.Kind,
			Detail:     e.Detail,
			TxBytes:    e.TxBytes,
			RxBytes:    e.RxBytes,
			RecordMode: e.Record,
		})
	}
}
