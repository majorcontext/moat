#!/usr/bin/env python3
"""
Firewall demo script

Demonstrates:
  1. Allowed request to httpbin.org (in allow list)
  2. Blocked request to example.com (not in allow list)
"""

import urllib.request
import urllib.error
import ssl

# Create SSL context that trusts the AgentOps CA
ssl_context = ssl.create_default_context()
ssl_context.check_hostname = False
ssl_context.verify_mode = ssl.CERT_NONE  # Trust proxy's generated certs

def make_request(url: str) -> tuple[int, str, dict]:
    """Make HTTP request, return (status_code, body, headers)"""
    try:
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req, timeout=10, context=ssl_context) as resp:
            return resp.status, resp.read().decode()[:500], dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(), dict(e.headers)
    except urllib.error.URLError as e:
        reason = str(e.reason)
        # Python wraps proxy 407 errors in a tunnel connection failure message
        if "407" in reason:
            return 407, reason, {}
        return 0, reason, {}

def main():
    print("=" * 50)
    print("Network Firewall Demo")
    print("=" * 50)
    print()
    print("Policy: strict")
    print("Allowed hosts: httpbin.org, *.httpbin.org")
    print()

    # Test 1: Allowed request
    print("-" * 50)
    print("Test 1: Request to httpbin.org (ALLOWED)")
    print("-" * 50)
    print()

    status, body, headers = make_request("https://httpbin.org/get")

    if status == 200:
        print(f"Status: {status} OK")
        print()
        # Show first few lines of response
        for line in body.split('\n')[:8]:
            print(f"  {line}")
        print("  ...")
        print()
        print("Result: SUCCESS - Request allowed")
    else:
        print(f"Status: {status}")
        print(f"Body: {body[:200]}")
        print()
        print("Result: UNEXPECTED - Request should have succeeded")

    print()
    print()

    # Test 2: Blocked request
    print("-" * 50)
    print("Test 2: Request to example.com (BLOCKED)")
    print("-" * 50)
    print()

    status, body, headers = make_request("https://example.com")

    if status == 407:
        print(f"Status: {status} Proxy Authentication Required")
        print()
        if "X-AgentOps-Blocked" in headers:
            print(f"Header: X-AgentOps-Blocked: {headers['X-AgentOps-Blocked']}")
        print()
        print("Response body:")
        for line in body.strip().split('\n'):
            print(f"  {line}")
        print()
        print("Result: SUCCESS - Request blocked by network policy")
    else:
        print(f"Status: {status}")
        print(f"Body: {body[:200]}")
        print()
        print("Result: UNEXPECTED - Request should have been blocked with 407")

    print()
    print()
    print("=" * 50)
    print("Demo complete!")
    print("=" * 50)
    print()
    print("The firewall blocked example.com because it's not in the allow list.")
    print("Only httpbin.org and *.httpbin.org are permitted.")

if __name__ == "__main__":
    main()
