#!/bin/sh
set -e

echo "=== Ollama Service Demo ==="
echo "MOAT_OLLAMA_URL=$MOAT_OLLAMA_URL"
echo

echo "--- Available models ---"
curl -s "$MOAT_OLLAMA_URL/api/tags" | jq .
echo

echo "--- Generating response ---"
curl -s -H 'Content-Type: application/json' \
  "$MOAT_OLLAMA_URL/api/generate" \
  -d '{"model":"qwen2.5-coder:1.5b","prompt":"Write hello world in Go","stream":false}' \
  | jq -r '.response // ("Error: " + .error)'
echo
