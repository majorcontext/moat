#!/usr/bin/env python3
"""Claude Code PostToolUse hook: runs gofmt on .go files after Edit/Write."""

import json
import subprocess
import sys


def main():
    data = json.load(sys.stdin)

    file_path = data.get("tool_input", {}).get("file_path", "")
    if not file_path or not file_path.endswith(".go"):
        sys.exit(0)

    subprocess.run(["gofmt", "-w", file_path], capture_output=True)


if __name__ == "__main__":
    main()
