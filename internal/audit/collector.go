package audit

import (
	"bufio"
	"encoding/json"
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

	// Set write-only permissions (0222)
	os.Chmod(socketPath, 0222)

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

		c.store.Append(entryType, msg.Data)
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
