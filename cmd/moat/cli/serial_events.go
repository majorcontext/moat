package cli

import (
	"path/filepath"
	"sync"
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
// Sinks are opened lazily and cached, because the daemon outlives the runs and
// cannot know at startup which runs will use devices.
type serialEventFanout struct {
	baseDir string

	storeMu sync.Mutex
	stores  map[string]*storage.RunStore

	auditMu     sync.Mutex
	auditStores map[string]*audit.Store
}

func newSerialEventFanout(baseDir string) *serialEventFanout {
	return &serialEventFanout{
		baseDir:     baseDir,
		stores:      make(map[string]*storage.RunStore),
		auditStores: make(map[string]*audit.Store),
	}
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

// runStore returns the run's storage, opening and caching it on first use.
func (f *serialEventFanout) runStore(runID string) *storage.RunStore {
	f.storeMu.Lock()
	defer f.storeMu.Unlock()
	store, ok := f.stores[runID]
	if !ok {
		var err error
		store, err = storage.NewRunStore(f.baseDir, runID)
		if err != nil {
			log.Warn("failed to open run store for device log",
				"run_id", runID, "error", err)
			return nil
		}
		f.stores[runID] = store
	}
	return store
}

// auditStore returns the run's audit store, opening and caching it on first
// use.
func (f *serialEventFanout) auditStore(runID string) *audit.Store {
	f.auditMu.Lock()
	defer f.auditMu.Unlock()
	as, ok := f.auditStores[runID]
	if !ok {
		var err error
		as, err = audit.OpenStore(filepath.Join(f.baseDir, runID, "audit.db"))
		if err != nil {
			log.Warn("failed to open audit store for device log",
				"run_id", runID, "error", err)
			return nil
		}
		f.auditStores[runID] = as
	}
	return as
}
