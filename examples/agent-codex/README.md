# Codex Example

Run OpenAI Codex CLI in an isolated container with automatic API key injection.

## Prerequisites

1. An OpenAI API key (get one at https://platform.openai.com/) or a ChatGPT Pro/Teams subscription
2. Moat built and in your PATH

## Setup (One-Time)

Store your OpenAI credentials securely:

```bash
# Option 1: API key
export OPENAI_API_KEY="sk-..."
moat grant openai

# Option 2: ChatGPT subscription (if codex CLI is installed)
moat grant openai
# Select option 1 for ChatGPT subscription login
```

Expected output:

```
Validating API key...
API key is valid.

OpenAI API key saved to ~/.moat/credentials/openai
```

This validates the key and stores it encrypted. The key is never passed to the container directly—it's injected at the network layer by the proxy.

## Running Codex

### Interactive Mode

Start an interactive Codex session:

```bash
moat codex examples/agent-codex
```

Codex will start in `/workspace` with your project files mounted.

### One-Shot Mode (Headless)

Ask Codex to analyze or fix code without interaction using `-p`:

```bash
# Analyze the code
moat codex examples/agent-codex -p "what does this code do?"

# Fix the bug
moat codex examples/agent-codex -p "fix the bug in main.py"
```

By default, one-shot mode uses `--full-auto` since the container provides isolation.
Use `--noyolo` to require manual approval for each action:

```bash
moat codex examples/agent-codex -p "fix the bug" --noyolo
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

Ask Codex to fix it and verify the output.

## What's Happening

1. Moat creates an isolated container with Node.js 20 and Codex CLI
2. A TLS-intercepting proxy starts and injects your credentials into requests to `api.openai.com` and `chatgpt.com`
3. Codex runs inside the container, unaware of the real API key
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
  "url": "https://api.openai.com/v1/responses",
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
