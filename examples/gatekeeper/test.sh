#!/usr/bin/env bash
# Test the Gate Keeper proxy with curl.
#
# Prerequisites:
#   1. Run ./run.sh in another terminal
#   2. Set GITHUB_TOKEN env var
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
echo "(GITHUB_TOKEN should be set for credential injection)"
echo ""
curl -s --cacert "$CA_CERT" --proxy "http://127.0.0.1:9080" https://api.github.com/user | head -20
