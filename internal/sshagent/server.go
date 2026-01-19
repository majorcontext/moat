package sshagent

import (
	"fmt"
	"net"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Server listens for SSH agent protocol connections and proxies requests.
// It can listen on either a Unix socket or TCP, depending on configuration.
type Server struct {
	proxy      *Proxy
	socketPath string // Unix socket path (empty if using TCP)
	tcpAddr    string // TCP address (empty if using Unix socket)
	listener   net.Listener
	wg         sync.WaitGroup
	done       chan struct{}
	closeOnce  sync.Once
}

// NewServer creates a new SSH agent server listening on a Unix socket.
func NewServer(proxy *Proxy, socketPath string) *Server {
	return &Server{
		proxy:      proxy,
		socketPath: socketPath,
		done:       make(chan struct{}),
	}
}

// NewTCPServer creates a new SSH agent server listening on TCP.
// The addr should be in the form "host:port" or ":port".
func NewTCPServer(proxy *Proxy, addr string) *Server {
	return &Server{
		proxy:   proxy,
		tcpAddr: addr,
		done:    make(chan struct{}),
	}
}

// SocketPath returns the path to the Unix socket (empty if using TCP).
func (s *Server) SocketPath() string {
	return s.socketPath
}

// TCPAddr returns the TCP address the server is listening on.
// Returns empty string if not using TCP or not yet started.
func (s *Server) TCPAddr() string {
	if s.listener != nil && s.tcpAddr != "" {
		return s.listener.Addr().String()
	}
	return s.tcpAddr
}

// Proxy returns the underlying filtering proxy.
func (s *Server) Proxy() *Proxy {
	return s.proxy
}

// Start begins listening on either Unix socket or TCP, depending on configuration.
func (s *Server) Start() error {
	if s.tcpAddr != "" {
		return s.startTCP()
	}
	return s.startUnix()
}

// startUnix begins listening on a Unix socket.
func (s *Server) startUnix() error {
	// Remove existing socket if present
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listening on socket: %w", err)
	}
	s.listener = listener

	// Set socket permissions to allow container access. The container process
	// runs as a different user (different UID), so it needs world read/write.
	// Security is maintained by:
	// 1. The socket directory is per-run (~/.moat/sockets/<run-id>/)
	// 2. The proxy enforces host-based key filtering
	// 3. Only the specific container has the directory mounted
	if err := os.Chmod(s.socketPath, 0666); err != nil {
		listener.Close()
		return fmt.Errorf("setting socket permissions: %w", err)
	}

	s.wg.Add(1)
	go s.serve()

	return nil
}

// startTCP begins listening on a TCP address.
func (s *Server) startTCP() error {
	listener, err := net.Listen("tcp", s.tcpAddr)
	if err != nil {
		return fmt.Errorf("listening on TCP: %w", err)
	}
	s.listener = listener

	s.wg.Add(1)
	go s.serve()

	return nil
}

func (s *Server) serve() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(conn)
		}()
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Wrap our proxy in an adapter that implements agent.Agent
	adapter := &agentAdapter{proxy: s.proxy}
	_ = agent.ServeAgent(adapter, conn)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.listener != nil {
			s.listener.Close()
		}
	})
	s.wg.Wait()
	// Only remove socket file for Unix socket servers
	if s.socketPath != "" {
		os.Remove(s.socketPath)
	}
	return nil
}

// agentAdapter adapts our Proxy to implement agent.Agent interface.
type agentAdapter struct {
	proxy *Proxy
}

// List returns the identities known to the agent.
func (a *agentAdapter) List() ([]*agent.Key, error) {
	ids, err := a.proxy.List()
	if err != nil {
		return nil, err
	}

	keys := make([]*agent.Key, len(ids))
	for i, id := range ids {
		// Parse the key to extract the format (type string)
		// KeyBlob is in SSH wire format which includes the type prefix
		pubKey, err := ssh.ParsePublicKey(id.KeyBlob)
		format := "ssh-rsa" // fallback
		if err == nil {
			format = pubKey.Type()
		}
		keys[i] = &agent.Key{
			Format:  format,
			Blob:    id.KeyBlob,
			Comment: id.Comment,
		}
	}
	return keys, nil
}

// Sign has the agent sign the data using a protocol 2 key as identified by the blob.
func (a *agentAdapter) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	id := &Identity{
		KeyBlob: key.Marshal(),
	}
	sigBytes, err := a.proxy.Sign(id, data)
	if err != nil {
		return nil, err
	}

	// Parse the signature bytes back into ssh.Signature
	sig := &ssh.Signature{}
	if err := ssh.Unmarshal(sigBytes, sig); err != nil {
		// If unmarshal fails, return raw signature
		return &ssh.Signature{
			Format: key.Type(),
			Blob:   sigBytes,
		}, nil
	}
	return sig, nil
}

// Add adds a private key to the agent.
func (a *agentAdapter) Add(key agent.AddedKey) error {
	return fmt.Errorf("adding keys not supported by moat SSH proxy")
}

// Remove removes all identities with the given public key.
func (a *agentAdapter) Remove(key ssh.PublicKey) error {
	return fmt.Errorf("removing keys not supported by moat SSH proxy")
}

// RemoveAll removes all identities.
func (a *agentAdapter) RemoveAll() error {
	return fmt.Errorf("removing keys not supported by moat SSH proxy")
}

// Lock locks the agent.
func (a *agentAdapter) Lock(passphrase []byte) error {
	return fmt.Errorf("locking not supported by moat SSH proxy")
}

// Unlock undoes the effect of Lock.
func (a *agentAdapter) Unlock(passphrase []byte) error {
	return fmt.Errorf("unlocking not supported by moat SSH proxy")
}

// Signers returns signers for all keys in the agent.
func (a *agentAdapter) Signers() ([]ssh.Signer, error) {
	return nil, fmt.Errorf("signers not supported by moat SSH proxy")
}
