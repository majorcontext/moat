#!/usr/bin/env python3
"""Simple API server that responds with a greeting on port 8080."""

from http.server import HTTPServer, BaseHTTPRequestHandler
import os

PORT = 8080


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(f"Hello from port {PORT}! (api service)\n".encode())

    def log_message(self, format, *args):
        print(f"[api] {args[0]}")


if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", PORT), Handler)
    print(f"API server running on port {PORT}")
    print(f"MOAT_URL_API={os.environ.get('MOAT_URL_API', 'not set')}")
    server.serve_forever()
