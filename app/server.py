from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from app.api import GatewayApp


INDEX_HTML = """<!doctype html>
<html>
  <head>
    <meta charset=\"utf-8\" />
    <meta name=\"viewport\" content=\"width=device-width,initial-scale=1\" />
    <title>BD AI Gateway</title>
    <style>
      body { font-family: Arial, sans-serif; margin: 0; background: #f8fafc; color: #0f172a; }
      main { max-width: 780px; margin: 2rem auto; background: #fff; border-radius: 12px; padding: 1rem; box-shadow: 0 10px 30px rgba(0,0,0,.08); }
      h1 { margin-top: 0; }
      #chat { height: 360px; overflow-y: auto; border: 1px solid #cbd5e1; padding: .75rem; border-radius: 8px; }
      .row { margin-top: .8rem; display: flex; gap: .5rem; }
      input, select, button { padding: .55rem .7rem; border: 1px solid #94a3b8; border-radius: 8px; }
      input { flex: 1; }
      button { background: #0f766e; color: #fff; border: none; }
      small { color: #475569; }
    </style>
  </head>
  <body>
    <main>
      <h1>BD AI Gateway MVP</h1>
      <p>Auto-routed chat with credits.</p>
      <div id=\"chat\"></div>
      <div class=\"row\">
        <select id=\"model\">
          <option value=\"auto\">AUTO</option>
          <option value=\"fast-chat\">fast-chat</option>
          <option value=\"smart-reasoning\">smart-reasoning</option>
        </select>
        <input id=\"prompt\" placeholder=\"Ask anything...\" />
        <button id=\"send\">Send</button>
      </div>
      <p><small>API key used in demo: dev-user-1</small></p>
    </main>
    <script>
      const chat = document.getElementById('chat');
      const prompt = document.getElementById('prompt');
      const model = document.getElementById('model');
      function add(role, text) {
        const el = document.createElement('p');
        el.innerHTML = '<strong>' + role + ':</strong> ' + text;
        chat.appendChild(el);
        chat.scrollTop = chat.scrollHeight;
      }
      document.getElementById('send').onclick = async () => {
        const value = prompt.value.trim();
        if (!value) return;
        add('you', value);
        prompt.value = '';
        const res = await fetch('/v1/chat/completions', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'x-api-key': 'dev-user-1' },
          body: JSON.stringify({ model: model.value, task_type: 'chat', messages: [{ role: 'user', content: value }] })
        });
        const data = await res.json();
        add('assistant', data.choices[0].message.content + ' (model: ' + data.model + ')');
      };
    </script>
  </body>
</html>
"""


class Handler(BaseHTTPRequestHandler):
    app = GatewayApp()

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/":
            payload = INDEX_HTML.encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return
        status, headers, body = self.app.handle("GET", self.path, dict(self.headers.items()), None)
        self._reply(status, headers, body)

    def do_POST(self) -> None:  # noqa: N802
        content_length = int(self.headers.get("Content-Length", "0"))
        payload = self.rfile.read(content_length).decode("utf-8") if content_length else None
        status, headers, body = self.app.handle("POST", self.path, dict(self.headers.items()), payload)
        self._reply(status, headers, body)

    def _reply(self, status: int, headers: dict, body: str) -> None:
        encoded = body.encode("utf-8")
        self.send_response(status)
        for key, value in headers.items():
            self.send_header(key, value)
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, format: str, *args) -> None:  # noqa: A003
        _ = format
        _ = args


def run(host: str = "127.0.0.1", port: int = 8080) -> None:
    server = ThreadingHTTPServer((host, port), Handler)
    try:
        print(f"BD AI Gateway listening on http://{host}:{port}")
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    run()
