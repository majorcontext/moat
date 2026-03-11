#!/usr/bin/env python3
"""
Firewall demo script — HTTP request rules

Demonstrates fine-grained network rules with method + path matching:
  1. GET to httpbin.org      → allowed (matches "allow GET /**")
  2. POST to httpbin.org     → blocked (matches "deny * /**")
  3. GET to api.github.com   → allowed (matches "allow GET /repos/*")
  4. DELETE to api.github.com → blocked (matches "deny * /**")
  5. GET /admin on github    → blocked (matches "deny * /admin/**")
  6. Request to example.com  → blocked (host not in rules, strict policy)
  7. Direct socket bypass    → blocked (iptables firewall)
"""

import socket
import urllib.request
import urllib.error
import ssl

# Trust the Moat proxy's generated TLS certs
ssl_context = ssl.create_default_context()
ssl_context.check_hostname = False
ssl_context.verify_mode = ssl.CERT_NONE


def make_request(url: str, method: str = "GET") -> tuple[int, str, dict]:
    """Make HTTP request, return (status_code, body, headers)."""
    try:
        req = urllib.request.Request(url, method=method)
        with urllib.request.urlopen(req, timeout=10, context=ssl_context) as resp:
            return resp.status, resp.read().decode()[:500], dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:500], dict(e.headers)
    except urllib.error.URLError as e:
        reason = str(e.reason)
        if "407" in reason:
            return 407, reason, {}
        return 0, reason, {}


def run_test(num: int, desc: str, url: str, method: str, expect_allowed: bool):
    """Run a single test and print the result."""
    print(f"Test {num}: {desc}")
    print(f"  {method} {url}")

    status, body, headers = make_request(url, method)

    blocked = status == 407 or status == 0
    allowed = not blocked

    if allowed == expect_allowed:
        result = "PASS"
    else:
        result = "FAIL"

    if allowed:
        print(f"  → {status} OK")
    elif status == 407:
        blocked_by = headers.get("X-Moat-Blocked", "network-policy")
        print(f"  → Blocked ({blocked_by})")
    else:
        print(f"  → Connection refused")

    label = "allowed" if expect_allowed else "blocked"
    print(f"  [{result}] Expected: {label}")
    print()
    return result == "PASS"


def main():
    print("=" * 55)
    print("  Network Firewall Demo — HTTP Request Rules")
    print("=" * 55)
    print()
    print("  Policy: strict (default deny)")
    print("  Rules: method + path patterns per host")
    print()

    results = []

    # --- httpbin.org: allow GET, deny everything else ---
    print("-" * 55)
    print("  httpbin.org — allow GET only")
    print("-" * 55)
    print()

    results.append(run_test(
        1, "GET allowed by 'allow GET /**'",
        "https://httpbin.org/get", "GET", expect_allowed=True,
    ))

    results.append(run_test(
        2, "POST blocked by 'deny * /**'",
        "https://httpbin.org/post", "POST", expect_allowed=False,
    ))

    # --- api.github.com: path-based rules ---
    print("-" * 55)
    print("  api.github.com — path-based access control")
    print("-" * 55)
    print()

    results.append(run_test(
        3, "GET /repos/moat allowed by 'allow GET /repos/*'",
        "https://api.github.com/repos/moat", "GET", expect_allowed=True,
    ))

    results.append(run_test(
        4, "DELETE blocked by 'deny * /**'",
        "https://api.github.com/repos/moat", "DELETE", expect_allowed=False,
    ))

    results.append(run_test(
        5, "GET /admin/users blocked by 'deny * /admin/**'",
        "https://api.github.com/admin/users", "GET", expect_allowed=False,
    ))

    # --- example.com: not in rules, strict policy blocks ---
    print("-" * 55)
    print("  example.com — unlisted host (strict = deny)")
    print("-" * 55)
    print()

    results.append(run_test(
        6, "Blocked by strict policy (host not in rules)",
        "https://example.com", "GET", expect_allowed=False,
    ))

    # --- Direct socket bypass ---
    print("-" * 55)
    print("  Direct socket — iptables blocks proxy bypass")
    print("-" * 55)
    print()
    print("Test 7: Raw socket to example.com:80")
    print("  Bypasses HTTP_PROXY env var")

    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(5)
        sock.connect(("93.184.216.34", 80))
        sock.close()
        print("  → Connected (unexpected)")
        print("  [FAIL] Expected: blocked")
        results.append(False)
    except (socket.timeout, OSError) as e:
        print(f"  → Blocked ({e})")
        print("  [PASS] Expected: blocked")
        results.append(True)

    # --- Summary ---
    print()
    print("=" * 55)
    passed = sum(results)
    total = len(results)
    print(f"  Results: {passed}/{total} tests passed")
    print("=" * 55)


if __name__ == "__main__":
    main()
