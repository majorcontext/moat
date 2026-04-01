#!/usr/bin/env bash
# Build and run the Gate Keeper proxy.
set -euo pipefail

cd "$(dirname "$0")"
ROOT="$(cd ../.. && pwd)"

# Generate CA if missing.
./gen-ca.sh

# Build the gatekeeper binary.
echo "Building gatekeeper..."
CGO_ENABLED=0 go build -o gatekeeper "${ROOT}/cmd/gatekeeper"

echo "Starting Gate Keeper with config: gatekeeper.yaml"
echo "Press Ctrl+C to stop."
echo ""
exec ./gatekeeper --config gatekeeper.yaml
