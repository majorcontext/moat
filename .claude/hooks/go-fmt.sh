#!/usr/bin/env bash
# Runs gofmt on .go files after they are edited or written by Claude Code.
set -euo pipefail

input=$(cat)
file=$(echo "$input" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('tool_input', {}).get('file_path', ''))" 2>/dev/null || true)

if [[ "$file" == *.go ]] && [[ -f "$file" ]]; then
    gofmt -w "$file" 2>/dev/null || true
fi
