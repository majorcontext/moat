#!/bin/sh
# Serial device smoke test for the LilyGO T-Display S3.
#
# Runs inside the moat container (see moat.yaml — its `command:` points
# here). Verifies the board is reachable through the RFC2217 broker and
# that esptool can drive the auto-reset lines into the bootloader.
set -e

echo "=== Device URL ==="
echo "MOAT_SERIAL_ESP32_URL=$MOAT_SERIAL_ESP32_URL"

echo ""
echo "=== esptool chip_id ==="
esptool --port "$MOAT_SERIAL_ESP32_URL" chip_id
