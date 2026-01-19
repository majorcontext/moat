# Claude Code Example

Run Claude Code in an isolated container with automatic API key injection.

## Prerequisites

1. An Anthropic API key (get one at https://console.anthropic.com/)
2. Moat built and in your PATH

## Setup (One-Time)

Store your Anthropic API key securely:

```bash
export ANTHROPIC_API_KEY="sk-ant-api03-..."
moat grant anthropic
```

Expected output:

```
Validating Anthropic API key...
✓ API key is valid
✓ Credential stored for anthropic
```

This validates the key and stores it encrypted. The key is never passed to the container directly—it's injected at the network layer by the proxy.

## Running Claude Code

### Interactive Mode

Start an interactive Claude Code session:

```bash
moat run my-agent examples/claude-code --grant anthropic -- npx @anthropic-ai/claude-code
```

Claude Code will start in `/workspace` with your project files mounted.

### One-Shot Mode (Headless)

Ask Claude to analyze or fix code without interaction using `-p` (print mode):

```bash
# Analyze the code
moat run my-agent examples/claude-code --grant anthropic -- npx @anthropic-ai/claude-code -p "what does this code do?"

# Fix the bug
moat run my-agent examples/claude-code --grant anthropic -- npx @anthropic-ai/claude-code -p "fix the bug in main.py"
```

## The Test Project

`main.py` contains a Fibonacci calculator with an intentional bug:

```python
return fibonacci(n - 1) + fibonacci(n - 3)  # Bug: should be n-2
```

Running it produces incorrect output:

```
$ python main.py
First 10 Fibonacci numbers:
  F(0) = 0
  F(1) = 1
  F(2) = 1
  F(3) = 1    # Wrong! Should be 2
  F(4) = 2    # Wrong! Should be 3
  ...
```

Ask Claude Code to fix it and verify the output.

## What's Happening

1. Moat creates an isolated container with Node.js 20
2. A TLS-intercepting proxy starts and injects your API key into requests to `api.anthropic.com`
3. Claude Code runs inside the container, unaware of the real API key
4. All network requests are logged for observability

## Viewing Logs

After a run completes, view the captured data:

```bash
# List runs
moat list
```

Expected output:

```
RUN ID                                STATUS    AGENT      DURATION
a1b2c3d4-e5f6-7890-abcd-ef1234567890  exited    my-agent   45s
```

```bash
# View network requests (shows all API calls)
cat ~/.moat/runs/<run-id>/network.jsonl | jq .
```

Example network log entry:

```json
{
  "ts": "2024-01-15T10:30:00Z",
  "method": "POST",
  "url": "https://api.anthropic.com/v1/messages",
  "status_code": 200,
  "duration_ms": 1234,
  "req_headers": {"Content-Type": "application/json"},
  "resp_headers": {"Content-Type": "application/json"}
}
```

```bash
# View console output
cat ~/.moat/runs/<run-id>/logs.jsonl
```
