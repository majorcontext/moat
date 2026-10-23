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

// Copy applies a container clipboard change to the host. Consume the paste echo
// once: a later agent copy of the same text must still update the host clipboard.
func (b *Bridge) Copy(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	echo := b.hasPaste && bytes.Equal(b.pasted, data)
	b.hasPaste = false
	b.pasted = nil
	if echo {
		return nil
	}
	return b.writeHost(data)
}

// Close prevents in-flight watcher callbacks from writing after detach.
func (b *Bridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
}
