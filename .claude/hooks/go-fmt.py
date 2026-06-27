#!/usr/bin/env python3
"""Claude Code PostToolUse hook: format .go files after Edit/Write.

Prefers gofumpt (the stricter formatter CI's golangci-lint enforces) when it's
installed, and falls back to gofmt (always available with the Go toolchain).
gofumpt is a superset of gofmt, so the fallback still formats — CI just enforces
the remaining stricter rules.
"""

import json
import shutil
import subprocess
import sys


def main():
    data = json.load(sys.stdin)

    file_path = data.get("tool_input", {}).get("file_path", "")
    if not file_path or not file_path.endswith(".go"):
        sys.exit(0)

    formatter = "gofumpt" if shutil.which("gofumpt") else "gofmt"
    subprocess.run([formatter, "-w", file_path], capture_output=True)


if __name__ == "__main__":
    main()
