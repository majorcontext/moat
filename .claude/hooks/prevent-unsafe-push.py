#!/usr/bin/env python3
"""Claude Code PreToolUse hook: blocks git push to main and force pushes."""

import json
import re
import sys

def main():
    data = json.load(sys.stdin)

    if data.get("tool_name") != "Bash":
        sys.exit(0)

    command = data.get("tool_input", {}).get("command", "")
    if not command:
        sys.exit(0)

    # Check for git push anywhere in the command (handles && and ; chains)
    if not re.search(r"\bgit\s+push\b", command):
        sys.exit(0)

    # Block force push (--force or -f, but not --force-with-lease)
    if re.search(r"\bgit\s+push\b.*(\s+--force(?!-with-lease)\b|\s+-f\b)", command):
        print("Force push is not allowed. Use --force-with-lease if necessary.", file=sys.stderr)
        sys.exit(2)

    # Block push to main
    if re.search(r"\bgit\s+push\b.*\s+main\b", command):
        print("Direct push to main is not allowed. Create a pull request instead.", file=sys.stderr)
        sys.exit(2)

if __name__ == "__main__":
    main()
