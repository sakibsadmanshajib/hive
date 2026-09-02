"""Minimal OpenAI-shaped upstream for the Cowork step streaming capture.

The chat front end needs a non-empty model list before its composer will send
anything, and that list comes from its own backend's upstream call rather than
from the browser. Nothing else here is exercised: the run under capture never
reaches a model, because a Cowork submission goes to the agent API, which the
capture script intercepts in the browser.
"""

import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

MODELS = {
    "object": "list",
    "data": [{"id": "hive-default", "object": "model", "created": 0, "owned_by": "hive"}],
}


class Handler(BaseHTTPRequestHandler):
    def _send(self, payload, status=200):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.rstrip("/").endswith("/models"):
            self._send(MODELS)
            return
        self._send({"error": "not found"}, 404)

    def do_POST(self):
        self._send({"error": "the capture never calls a model"}, 400)

    def log_message(self, fmt, *args):
        sys.stderr.write("stub %s\n" % (fmt % args))


if __name__ == "__main__":
    HTTPServer(("0.0.0.0", int(sys.argv[1])), Handler).serve_forever()
