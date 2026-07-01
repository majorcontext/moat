# Pi Example

Run the [Pi coding agent](https://github.com/earendil-works/pi) in an isolated container with automatic API key injection.

Pi has no credential of its own — it runs against your `anthropic` or `openai` grant. Only those two backends are supported today.

## Prerequisites

1. An Anthropic API key (get one at https://console.anthropic.com/) **or** an OpenAI API key (https://platform.openai.com/)
2. Moat built and in your PATH

## Setup (One-Time)

Store a backend credential securely:

```bash
# Anthropic
export ANTHROPIC_API_KEY="sk-ant-..."
moat grant anthropic

# ...or OpenAI
export OPENAI_API_KEY="sk-..."
moat grant openai
```

This validates the key and stores it encrypted. The key is never passed to the container directly — it's injected at the network layer by the proxy.

## Choosing a backend

- If exactly **one** of the `anthropic` / `openai` grants is configured, Pi uses it automatically.
- If **both** are configured, choose one with `--provider` or `pi.provider`:

  ```bash
  moat pi examples/agent-pi --provider openai
  ```

- Requesting any other backend (or running with no supported grant) fails immediately, before a container is created.

## Running Pi

### Interactive Mode

```bash
moat pi examples/agent-pi
```

Pi starts in `/workspace` with your project files mounted.

### One-Shot Mode (Headless)

```bash
# Analyze the code
moat pi examples/agent-pi -p "what does this code do?"

# Fix the bug
moat pi examples/agent-pi -p "fix the bug in main.py"
```

### Pinning a model

```bash
moat pi examples/agent-pi --provider anthropic --model claude-opus-4-8
```

## The Test Project

`main.py` contains a Fibonacci calculator with an intentional bug (`n - 3` should be `n - 2`), so `F(5)` returns `3` instead of `5`. Ask Pi to find and fix it.

## Observability

```bash
# View network requests (credentials are injected at the proxy, never shown here)
cat ~/.moat/runs/<run-id>/network.jsonl

# View console output
cat ~/.moat/runs/<run-id>/logs.jsonl
```
