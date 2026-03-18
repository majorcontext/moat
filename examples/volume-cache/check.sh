#!/bin/sh
# Volume persistence check
#
# Writes a timestamp on first run, reads it back on subsequent runs.

STAMP="/workspace/.cache/created-at"

if [ -f "$STAMP" ]; then
    echo "Found cached state from: $(cat "$STAMP")"
else
    NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    echo "$NOW" > "$STAMP"
    echo "No cached state found. Writing timestamp: $NOW"
fi
