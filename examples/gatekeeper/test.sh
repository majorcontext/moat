#!/usr/bin/env bash
# Test the Gate Keeper proxy with curl.
#
# Prerequisites:
#   1. Set GITHUB_TOKEN in the run.sh terminal: export GITHUB_TOKEN="token ghp_..."
#   2. Run ./run.sh in that terminal (reads GITHUB_TOKEN at startup)
set -euo pipefail

cd "$(dirname "$0")"

# Trust the example CA for curl.
CA_CERT="$(pwd)/ca.crt"
if [ ! -f "$CA_CERT" ]; then
  echo "Error: ca.crt not found. Run ./gen-ca.sh or ./run.sh first." >&2
  exit 1
fi

echo "=== Health check ==="
curl -s "http://127.0.0.1:9080/healthz"
echo ""

echo ""
echo "=== Credential injection ==="
echo "Requesting https://api.github.com/user through proxy..."
echo "(GITHUB_TOKEN must be set in the run.sh terminal, not here)"
echo ""
curl -s --cacert "$CA_CERT" --proxy "http://127.0.0.1:9080" https://api.github.com/user | head -20
