"""Stand-in for control-plane's internal chat balance route, for the #1694 capture.

The chat front end's credits shim (deploy/docker/owui-patches/hive_credits.py)
POSTs to /internal/chat/credits/balance behind an internal token and passes the
answer to the banner and the Usage tab. This returns one fixed balance so the
capture exercises the real Svelte components with a real HTTP round trip,
without standing up a ledger.
"""

import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

BALANCE = {"available_credits": 9_996_364_207, "usage_today_credits": 340_000_000}


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("content-length") or 0)
        self.rfile.read(length)
        body = json.dumps(BALANCE).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        sys.stderr.write("stub %s\n" % (fmt % args))


HTTPServer(("0.0.0.0", int(sys.argv[1])), Handler).serve_forever()
