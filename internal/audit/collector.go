package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
)

// CollectorMessage is the wire format for log messages from agents.
type CollectorMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Collector receives log messages and stores them with hash chaining.
type Collector struct {
	store    *Store
	listener net.Listener
	done     chan struct{}
	wg       sync.WaitGroup
}

// NewCollector creates a new log collector.
func NewCollector(store *Store) *Collector {
	return &Collector{
		store: store,
		done:  make(chan struct{}),
	}
}

// StartUnix starts the collector listening on a Unix socket.
func (c *Collector) StartUnix(socketPath string) error {
	// Remove existing socket file
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	c.listener = listener

	// Set write-only permissions (0222) so agents can write logs but cannot
	// read them back. This is part of the tamper-proof security model.
	if err := os.Chmod(socketPath, 0222); err != nil {
		listener.Close()
		return fmt.Errorf("setting socket permissions: %w", err)
	}

	c.wg.Add(1)
	go c.acceptLoop()

	return nil
}

func (c *Collector) acceptLoop() {
	defer c.wg.Done()

	for {
		conn, err := c.listener.Accept()
		if err != nil {
			select {
			case <-c.done:
				return
			default:
				continue
			}
		}

		c.wg.Add(1)
		go c.handleConnection(conn)
	}
}

func (c *Collector) handleConnection(conn net.Conn) {
	defer c.wg.Done()
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var msg CollectorMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		entryType := EntryType(msg.Type)
		if entryType == "" {
			entryType = EntryConsole
		}

		if _, err := c.store.Append(entryType, msg.Data); err != nil {
			// Log error but continue processing - don't crash on storage errors
			// TODO: Add structured logging here for observability
			continue
		}
	}
}

// Stop stops the collector and waits for all connections to close.
func (c *Collector) Stop() error {
	close(c.done)
	if c.listener != nil {
		c.listener.Close()
	}
	c.wg.Wait()
	return nil
}
