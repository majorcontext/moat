package sshagent

import (
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// realAgent wraps golang.org/x/crypto/ssh/agent to implement AgentClient.
type realAgent struct {
	conn  net.Conn
	agent agent.ExtendedAgent
}

// ConnectAgent connects to an SSH agent at the given socket path.
func ConnectAgent(socketPath string) (AgentClient, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dialing SSH agent: %w", err)
	}
	return &realAgent{
		conn:  conn,
		agent: agent.NewClient(conn),
	}, nil
}

// List returns all identities from the SSH agent.
func (a *realAgent) List() ([]*Identity, error) {
	keys, err := a.agent.List()
	if err != nil {
		return nil, fmt.Errorf("listing keys: %w", err)
	}
	identities := make([]*Identity, len(keys))
	for i, k := range keys {
		// agent.Key.Blob is the SSH wire format (same as ssh.PublicKey.Marshal())
		identities[i] = &Identity{
			KeyBlob: k.Blob,
			Comment: k.Comment,
		}
	}
	return identities, nil
}

// Sign requests the agent to sign data using the specified key.
func (a *realAgent) Sign(key *Identity, data []byte) ([]byte, error) {
	// KeyBlob is in SSH wire format, same as what ssh.ParsePublicKey expects
	pubKey, err := ssh.ParsePublicKey(key.KeyBlob)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	sig, err := a.agent.Sign(pubKey, data)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	return ssh.Marshal(sig), nil
}

// Close closes the connection to the SSH agent.
func (a *realAgent) Close() error {
	return a.conn.Close()
}
