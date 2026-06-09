#!/usr/bin/env python3
"""Mock upstream HTTP server for the SRE + Backend take-home.

Deterministic behaviors candidates can exercise from their fetch logic.

Endpoints:
  GET /echo/<status>      Return the given HTTP status with an empty body.
  GET /slow/<ms>          Sleep <ms> milliseconds, then return 200.
  GET /flaky/<percent>    Return 503 with probability <percent>% (0-100), else 200.
                          Uses a deterministic hash of the URL path so retries
                          on the same path get the same answer (tests dedup) —
                          add ?seed=<x> to vary.
  GET /timeout            Sleep 30s. Use to verify your client deadlines kick in.
  GET /large/<kb>         Return <kb> KB of body. Tests memory / streaming.
  GET /health             Always 200. Used by docker-compose healthcheck.

Run:
  python3 server.py [PORT]    (default 8080)
"""

import hashlib
import os
import random
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        # Structured-ish log to stderr so docker logs is useful.
        sys.stderr.write(
            f"[mock-upstream] {self.address_string()} {self.command} {self.path} -> {fmt % args}\n"
        )

    def _write(self, status: int, body: bytes = b"", content_type: str = "text/plain"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if body:
            self.wfile.write(body)

    def do_GET(self):
        url = urlparse(self.path)
        parts = [p for p in url.path.split("/") if p]
        qs = parse_qs(url.query)

        if not parts:
            return self._write(200, b"mock-upstream up\n")

        kind = parts[0]

        if kind == "health":
            return self._write(200, b"ok\n")

        if kind == "echo" and len(parts) >= 2:
            try:
                status = int(parts[1])
            except ValueError:
                return self._write(400, b"bad status\n")
            body = b""
            if len(parts) >= 3:
                body = ("/".join(parts[2:]) + "\n").encode("utf-8")
            return self._write(status, body)

        if kind == "slow" and len(parts) >= 2:
            try:
                ms = max(0, int(parts[1]))
            except ValueError:
                return self._write(400, b"bad ms\n")
            time.sleep(ms / 1000.0)
            return self._write(200, f"slow {ms}ms\n".encode())

        if kind == "flaky" and len(parts) >= 2:
            try:
                pct = max(0, min(100, int(parts[1])))
            except ValueError:
                return self._write(400, b"bad percent\n")
            # Deterministic per (path, seed) so candidates can test retry repeatably.
            seed = (qs.get("seed", ["0"])[0]).encode()
            h = hashlib.sha256(self.path.encode() + seed).digest()
            roll = h[0] / 255.0 * 100.0  # 0..100
            if roll < pct:
                return self._write(503, b"flaky failure\n")
            return self._write(200, b"flaky ok\n")

        if kind == "timeout":
            time.sleep(30)
            return self._write(200, b"never\n")

        if kind == "large" and len(parts) >= 2:
            try:
                kb = max(0, min(10_000, int(parts[1])))
            except ValueError:
                return self._write(400, b"bad kb\n")
            chunk = b"A" * 1024
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(kb * 1024))
            self.end_headers()
            for _ in range(kb):
                self.wfile.write(chunk)
            return

        return self._write(404, b"unknown route\n")


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else int(os.environ.get("PORT", "8080"))
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    sys.stderr.write(f"[mock-upstream] listening on :{port}\n")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        sys.stderr.write("[mock-upstream] shutting down\n")
        server.shutdown()


if __name__ == "__main__":
    main()
