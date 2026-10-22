package clipboard

import (
	"bytes"
	"context"
	"sync"
)

// Bridge serializes host paste and container copy operations. A paste echoed
// back by the X11 watcher must not overwrite a newer host clipboard selection.
type Bridge struct {
	mu             sync.Mutex
	closed         bool
	pasted         []byte
	hasPaste       bool
	readHost       func() (*Content, error)
	writeHost      func([]byte) error
	writeContainer func(context.Context, []byte, string) error
}

func NewBridge(writeContainer func(context.Context, []byte, string) error) *Bridge {
	return &Bridge{readHost: Read, writeHost: WriteText, writeContainer: writeContainer}
}

// Paste transfers the host clipboard before forwarding Ctrl+V to the agent.
func (b *Bridge) Paste(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || ctx.Err() != nil {
		return ctx.Err()
	}
	content, err := b.readHost()
	if err != nil || content == nil {
		return err
	}
	if err := b.writeContainer(ctx, content.Data, MIMEToXclipTarget(content.MIMEType)); err != nil {
		return err
	}
	b.hasPaste = !content.IsImage()
	if b.hasPaste {
		b.pasted = bytes.Clone(content.Data)
	} else {
		b.pasted = nil
	}
	return nil
}

// Copy applies a container clipboard change to the host. Repeated notifications
// for the last paste are ignored until the container copies different text.
func (b *Bridge) Copy(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || (b.hasPaste && bytes.Equal(b.pasted, data)) {
		return nil
	}
	b.hasPaste = false
	b.pasted = nil
	return b.writeHost(data)
}

// Close prevents in-flight watcher callbacks from writing after detach.
func (b *Bridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
}
