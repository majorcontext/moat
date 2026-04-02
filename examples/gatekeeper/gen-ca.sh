#!/usr/bin/env bash
# Generate a self-signed CA certificate for TLS interception.
# The proxy uses this CA to dynamically sign certificates for
# upstream hosts so it can inspect and modify HTTPS traffic.
set -euo pipefail

cd "$(dirname "$0")"

if [ -f ca.crt ] && [ -f ca.key ]; then
  echo "CA certificate already exists (ca.crt, ca.key). Remove them to regenerate."
  exit 0
fi

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -days 365 -nodes \
  -keyout ca.key -out ca.crt \
  -subj "/CN=Gate Keeper Example CA" \
  -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  2>/dev/null

chmod 0600 ca.key
echo "Generated ca.crt and ca.key"
