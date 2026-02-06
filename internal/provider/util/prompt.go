package util

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// PromptForToken prompts the user for a token with the given message.
// Input is hidden (not echoed to terminal).
func PromptForToken(prompt string) (string, error) {
	fmt.Print(prompt + ": ")

	// Check if stdin is a terminal
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		bytes, err := term.ReadPassword(fd)
		fmt.Println() // Add newline after hidden input
		if err != nil {
			return "", fmt.Errorf("failed to read token: %w", err)
		}
		return strings.TrimSpace(string(bytes)), nil
	}

	// Fallback for non-terminal (piped input)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read token: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// PromptForChoice displays options and returns the selected index (0-based).
// Returns -1 and error if input is invalid.
func PromptForChoice(prompt string, options []string) (int, error) {
	fmt.Println(prompt)
	for i, opt := range options {
		fmt.Printf("  %d. %s\n", i+1, opt)
	}
	fmt.Print("Enter choice: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return -1, fmt.Errorf("failed to read choice: %w", err)
	}

	var choice int
	if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &choice); err != nil {
		return -1, fmt.Errorf("invalid choice: %w", err)
	}

	if choice < 1 || choice > len(options) {
		return -1, fmt.Errorf("choice %d out of range [1-%d]", choice, len(options))
	}

	return choice - 1, nil
}

// Confirm prompts for yes/no confirmation. Returns true for yes.
func Confirm(prompt string) (bool, error) {
	fmt.Print(prompt + " [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read confirmation: %w", err)
	}

	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
