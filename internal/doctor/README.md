# Doctor Package

The `doctor` package provides a pluggable diagnostic system for Moat. It allows different components to register diagnostic sections that can be displayed via the `moat doctor` command.

## Architecture

The doctor system uses a simple registry pattern:

```go
type Section interface {
    Name() string
    Print(w io.Writer) error
}
```

Any package can implement a `Section` and register it with the doctor command. This makes the diagnostic system extensible without tight coupling.

## Usage

### Implementing a Doctor Section

To add diagnostic output for your package:

1. Implement the `doctor.Section` interface:

```go
package mypackage

import (
    "fmt"
    "io"
)

type DoctorSection struct{}

func (d *DoctorSection) Name() string {
    return "My Package Configuration"
}

func (d *DoctorSection) Print(w io.Writer) error {
    // Output diagnostic information
    fmt.Fprintln(w, "Status: ✅ OK")
    fmt.Fprintln(w, "Version: 1.0.0")
    return nil
}
```

2. Register it in the doctor command (`cmd/moat/cli/doctor.go`):

```go
reg.Register(&mypackage.DoctorSection{})
```

### Examples

See existing implementations:

- **Claude section** (`cmd/moat/cli/doctor.go:claudeSection`) - Shows Claude Code configuration including:
  - `~/.claude.json` config status
  - `~/.claude/settings.json` plugins and marketplaces
  - MCP server configuration

- **Codex section** (`internal/codex/doctor.go`) - Shows OpenAI Codex configuration:
  - Config directory status
  - Session information
  - Auth file warnings (should only exist in containers)

- **Credentials section** (`cmd/moat/cli/doctor.go:credentialSection`) - Shows stored credentials:
  - Token prefixes (safe to display)
  - JWT token claims (redacted)
  - Expiration status
  - OAuth scopes

## Design Principles

1. **Redact Sensitive Data** - Never output full tokens, secrets, or credentials. Show prefixes or redacted values.

2. **Actionable Output** - When something is wrong, tell the user exactly what to do to fix it.

3. **Graceful Degradation** - If a section fails, show an error but continue with other sections.

4. **Pluggable** - Packages should be able to add their own diagnostic output without modifying the core doctor command logic.

## Security

The doctor output is designed to be safe to share for debugging:

- **Tokens** are shown as prefixes only (e.g., `sk-ant-api03...`)
- **JWT claims** are redacted (showing first few characters of IDs)
- **Sensitive metadata** is filtered out
- **Expiration times** are shown as durations, not absolute timestamps

Never include full credentials, API keys, or other secrets in doctor output.
