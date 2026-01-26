#!/bin/bash
RUN_ID="${1:-$(./moat list --format json 2>/dev/null | jq -r '.[0].id // empty')}"

if [ -z "$RUN_ID" ]; then
  echo "No run ID found"
  exit 1
fi

echo "Checking run: $RUN_ID"
echo ""

NETWORK_FILE=~/.moat/runs/$RUN_ID/network.jsonl
if [ -f "$NETWORK_FILE" ]; then
  echo "=== Network requests to /mcp/ ==="
  grep '/mcp/' "$NETWORK_FILE" 2>/dev/null | jq -r '.url' 2>/dev/null || echo "No /mcp/ requests found"
  echo ""
  echo "=== All network requests (first 20) ==="
  jq -r '.url' "$NETWORK_FILE" 2>/dev/null | head -20
else
  echo "Network log file not found: $NETWORK_FILE"
fi
