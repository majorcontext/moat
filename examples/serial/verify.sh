#!/bin/bash
# Serial device smoke test for the LilyGO T-Display S3.
#
# Runs inside the moat container (see moat.yaml — its `command:` points
# here). Verifies the board is reachable through the RFC2217 broker and
# that esptool can drive the auto-reset lines into the bootloader.
#
# bash, not sh: the esptool pipeline needs `set -o pipefail` so a failed
# esptool is not masked by a successful awk.
set -e
set -o pipefail

echo "=== Device URL ==="
echo "MOAT_SERIAL_ESP32_URL=$MOAT_SERIAL_ESP32_URL"

echo ""
echo "=== esptool chip-id ==="
# esptool cannot learn the USB VID/PID of an rfc2217:// port — the device
# lives on the host, outside the container — so it prints "Failed to get
# VID/PID" on every lookup (it asks for USB IDs to pick a reset strategy,
# and caches the answer only on success). The fallback it then uses, the
# standard UART reset sequence, is the one that works over RFC2217, so the
# message is informational. This demo filters it out; run esptool directly
# to see it.
esptool --port "$MOAT_SERIAL_ESP32_URL" chip-id 2>&1 | awk '
    /Failed to get VID\/PID/ { skip = 1; next }
    skip && $0 == ""          { skip = 0; next }  # also drop the blank line after it
    { skip = 0; print }
'
