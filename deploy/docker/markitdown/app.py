"""Markitdown conversion sidecar for RAG binary document ingest.

Exposes POST /convert: raw file bytes in, markdown text out. Loud failure
contract: every failure is a non-2xx JSON error carrying an error class, and
an empty conversion result is itself a failure. An empty-text 200 is never
returned.

Stdlib HTTP only (no web framework); markitdown is the single dependency.
"""

import io
import json
import mimetypes
import os
import tempfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import markitdown._exceptions as mdx
from markitdown import MarkItDown

MAX_UPLOAD_BYTES = int(os.environ.get("MAX_UPLOAD_BYTES", str(25 * 1024 * 1024)))
PORT = int(os.environ.get("PORT", "8700"))

_converter = MarkItDown()


class ConversionError(Exception):
    """A conversion failure that must surface as a loud non-2xx JSON error."""

    def __init__(self, status, err_class, message):
        super().__init__(message)
        self.status = status
        self.err_class = err_class
        self.message = message


def _resolve_extension(filename, content_type):
    ext = os.path.splitext(filename or "")[1].lower()
    if ext:
        return ext
    guessed = mimetypes.guess_extension(content_type or "")
    if guessed:
        return guessed
    return ""


def convert_bytes(data: bytes, filename: str, content_type: str) -> str:
    """Convert raw file bytes to markdown. Raises ConversionError on failure."""
    if not data:
        raise ConversionError(400, "bad_request", "empty request body")

    ext = _resolve_extension(filename, content_type)
    if not ext:
        raise ConversionError(
            422,
            "unsupported_format",
            "no filename extension and no content type to infer one from",
        )

    # Write to a suffixed temp file so markitdown's own extension-based
    # converter dispatch picks the right reader; convert(path) works across
    # every markitdown release line.
    with tempfile.NamedTemporaryFile(suffix=ext) as tmp:
        tmp.write(data)
        tmp.flush()
        try:
            result = _converter.convert(tmp.name)
        except mdx.UnsupportedFormatException as exc:
            raise ConversionError(
                422, "unsupported_format", str(exc) or f"no converter for {ext}"
            ) from exc
        except (mdx.FileConversionException, mdx.MissingDependencyException) as exc:
            raise ConversionError(
                422, "conversion_failed", str(exc) or f"converter failed for {ext}"
            ) from exc
        except Exception as exc:  # noqa: BLE001 loud-fail boundary
            raise ConversionError(
                500, "conversion_failed", f"{type(exc).__name__}: {exc}"
            ) from exc

    text = result.text_content or ""
    if not text.strip():
        raise ConversionError(
            422, "empty_result", "converter produced no extractable text"
        )
    return text


class Handler(BaseHTTPRequestHandler):
    server_version = "hive-markitdown/1.0"
    # Keep-alive support requires an exact Content-Length on every response,
    # which every path below sets.
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        if self.path == "/healthz":
            body = json.dumps({"status": "ok"}).encode()
            self._send_json(200, body)
            return
        self._send_json(404, self._error_body(404, "not_found", "unknown path"))

    def do_POST(self):
        if self.path != "/convert":
            self._send_json(404, self._error_body(404, "not_found", "unknown path"))
            return

        try:
            length = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            length = -1
        if length <= 0:
            self._send_json(400, self._error_body(400, "bad_request", "missing Content-Length"))
            return
        if length > MAX_UPLOAD_BYTES:
            # Reject before reading the body so a huge upload never lands in
            # memory; close the connection since the unread body would
            # desynchronize keep-alive framing.
            self.close_connection = True
            self._send_json(
                413,
                self._error_body(
                    413, "payload_too_large",
                    f"body exceeds {MAX_UPLOAD_BYTES} bytes",
                ),
            )
            return

        try:
            data = self._read_capped(length)
        except ConnectionError:
            return
        if len(data) < length:
            self._send_json(400, self._error_body(400, "bad_request", "truncated body"))
            return

        filename = self.headers.get("X-Filename") or ""
        content_type = self.headers.get("Content-Type") or ""
        try:
            text = convert_bytes(data, filename, content_type)
        except ConversionError as exc:
            self._send_json(
                exc.status,
                self._error_body(exc.status, exc.err_class, exc.message),
            )
            return
        self._send_json(200, json.dumps({"markdown": text}).encode())

    def _read_capped(self, length):
        chunks = []
        remaining = length
        while remaining > 0:
            chunk = self.rfile.read(min(remaining, 65536))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        return b"".join(chunks)

    @staticmethod
    def _error_body(status, err_class, message):
        return json.dumps(
            {"error": {"code": status, "class": err_class, "message": message}}
        ).encode()

    def _send_json(self, status, body):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):  # noqa: A002 stdlib signature
        print("[markitdown] " + (format % args), flush=True)


def main():
    server = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    print(f"[markitdown] listening on :{PORT} cap={MAX_UPLOAD_BYTES}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
