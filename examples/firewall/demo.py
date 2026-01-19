#!/usr/bin/env python3
"""
Firewall demo script

Demonstrates:
  1. Allowed request to httpbin.org (in allow list)
  2. Blocked request to example.com (not in allow list)
  3. Direct socket connection blocked (bypasses proxy env vars)
"""

import socket
import urllib.request
import urllib.error
import ssl

# Create SSL context that trusts the Moat CA
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
        if "X-Moat-Blocked" in headers:
            print(f"Header: X-Moat-Blocked: {headers['X-Moat-Blocked']}")
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

    # Test 3: Direct socket connection (bypasses proxy)
    print("-" * 50)
    print("Test 3: Direct socket to example.com:80 (BLOCKED)")
    print("-" * 50)
    print()
    print("This test bypasses HTTP_PROXY by opening a raw socket.")
    print("The iptables firewall should block it.")
    print()

    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(5)
        # Try to connect directly to example.com
        sock.connect(("93.184.216.34", 80))  # example.com's IP
        sock.close()
        print("Status: Connected successfully")
        print()
        print("Result: UNEXPECTED - Direct socket should have been blocked")
    except socket.timeout:
        print("Status: Connection timed out")
        print()
        print("Result: SUCCESS - Firewall blocked direct connection")
    except OSError as e:
        print(f"Status: Connection failed: {e}")
        print()
        print("Result: SUCCESS - Firewall blocked direct connection")

    print()
    print()
    print("=" * 50)
    print("Demo complete!")
    print("=" * 50)
    print()
    print("The firewall blocked example.com because it's not in the allow list.")
    print("Only httpbin.org and *.httpbin.org are permitted.")
    print()
    print("Direct socket connections are also blocked by iptables rules,")
    print("preventing bypass of the HTTP proxy.")

if __name__ == "__main__":
    main()
