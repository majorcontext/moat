// cmd/moat/cli/grant_ssh.go
package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybons/moat/internal/credential"
	"github.com/andybons/moat/internal/sshagent"
	"github.com/spf13/cobra"
)

var grantSSHCmd = &cobra.Command{
	Use:   "ssh",
	Short: "Grant SSH access for a host",
	Long: `Grant SSH access for a specific host.

The container will be able to use SSH keys to authenticate to the specified host.
By default, uses the first key from your SSH agent. Use --key to specify a different key.

Examples:
  # Grant SSH access to github.com using the default key
  moat grant ssh --host github.com

  # Grant SSH access to gitlab.com using a specific key
  moat grant ssh --host gitlab.com --key ~/.ssh/work_key

  # Use SSH in a run
  moat run --grant ssh:github.com -- git clone git@github.com:org/repo.git`,
	RunE: runGrantSSH,
}

var (
	sshHost    string
	sshKeyPath string
)

func init() {
	grantCmd.AddCommand(grantSSHCmd)

	grantSSHCmd.Flags().StringVar(&sshHost, "host", "", "SSH host (e.g., github.com)")
	grantSSHCmd.Flags().StringVar(&sshKeyPath, "key", "", "Path to SSH private key (optional, uses agent's first key by default)")
	_ = grantSSHCmd.MarkFlagRequired("host")
}

func runGrantSSH(cmd *cobra.Command, args []string) error {
	// Connect to SSH agent
	agentSocket := os.Getenv("SSH_AUTH_SOCK")
	if agentSocket == "" {
		return fmt.Errorf("SSH_AUTH_SOCK not set\n\n" +
			"Your SSH agent must be running to grant SSH access.\n" +
			"Start it with: eval \"$(ssh-agent -s)\" && ssh-add")
	}

	agent, err := sshagent.ConnectAgent(agentSocket)
	if err != nil {
		return fmt.Errorf("connecting to SSH agent: %w\n\n"+
			"Make sure your SSH agent is running and SSH_AUTH_SOCK is set correctly.", err)
	}
	defer agent.Close()

	// List available keys
	identities, err := agent.List()
	if err != nil {
		return fmt.Errorf("listing SSH keys: %w", err)
	}
	if len(identities) == 0 {
		return fmt.Errorf("no SSH keys in agent\n\n" +
			"Add a key to your SSH agent:\n" +
			"  ssh-add ~/.ssh/id_ed25519\n" +
			"  ssh-add ~/.ssh/id_rsa")
	}

	// Find the key to use
	var selectedKey *sshagent.Identity
	if sshKeyPath != "" {
		// Find key matching the specified path
		keyPath := expandPath(sshKeyPath)
		pubKeyPath := keyPath
		if !strings.HasSuffix(pubKeyPath, ".pub") {
			pubKeyPath = keyPath + ".pub"
		}

		pubKeyData, readErr := os.ReadFile(pubKeyPath)
		if readErr != nil {
			return fmt.Errorf("reading public key %s: %w\n\n"+
				"Make sure the public key file exists.", pubKeyPath, readErr)
		}

		targetFP := fingerprintFromAuthorizedKey(pubKeyData)
		if targetFP == "" {
			return fmt.Errorf("could not parse public key from %s", pubKeyPath)
		}

		for _, id := range identities {
			if id.Fingerprint() == targetFP {
				selectedKey = id
				break
			}
		}
		if selectedKey == nil {
			return fmt.Errorf("key %s not found in SSH agent\n\n"+
				"Add it with: ssh-add %s", sshKeyPath, keyPath)
		}
	} else {
		// Use first available key
		selectedKey = identities[0]
		fmt.Printf("Using key: %s\n", selectedKey.Comment)
	}

	// Get credential store
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return fmt.Errorf("getting encryption key: %w", err)
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return fmt.Errorf("opening credential store: %w", err)
	}

	// Store the mapping
	mapping := credential.SSHMapping{
		Host:           sshHost,
		KeyFingerprint: selectedKey.Fingerprint(),
		KeyPath:        sshKeyPath,
	}
	if err := store.AddSSHMapping(mapping); err != nil {
		return fmt.Errorf("storing SSH mapping: %w", err)
	}

	fmt.Printf("\nGranted SSH access to %s\n", sshHost)
	fmt.Printf("  Key: %s\n", selectedKey.Fingerprint())
	if selectedKey.Comment != "" {
		fmt.Printf("  Comment: %s\n", selectedKey.Comment)
	}
	fmt.Printf("\nUse in runs with: moat run --grant ssh:%s\n", sshHost)

	return nil
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// fingerprintFromAuthorizedKey parses an authorized_keys format line and returns fingerprint.
// Format: "ssh-ed25519 AAAA... comment"
func fingerprintFromAuthorizedKey(data []byte) string {
	line := strings.TrimSpace(string(data))
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return ""
	}

	// parts[0] is key type, parts[1] is base64 encoded key blob
	// The fingerprint is computed from the raw key blob (decoded base64)
	keyBlob, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	return sshagent.Fingerprint(keyBlob)
}
