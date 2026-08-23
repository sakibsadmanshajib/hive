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
import re
from concurrent.futures import ThreadPoolExecutor, TimeoutError as FutureTimeout
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import markitdown._exceptions as mdx
from markitdown import MarkItDown

MAX_UPLOAD_BYTES = int(os.environ.get("MAX_UPLOAD_BYTES", str(25 * 1024 * 1024)))
PORT = int(os.environ.get("PORT", "8700"))
# Bounded conversion pool: a conversion that hangs cannot grow threads without
# limit. Workers that exceed CONVERT_TIMEOUT_SECONDS are abandoned (the thread
# finishes eventually and is discarded); the caller gets a loud 422.
CONVERT_TIMEOUT_SECONDS = float(os.environ.get("CONVERT_TIMEOUT_SECONDS", "120"))
MAX_WORKERS = int(os.environ.get("MAX_WORKERS", "2"))

_convert_pool = ThreadPoolExecutor(max_workers=MAX_WORKERS)

_converter = MarkItDown()

# Anything shaped like an absolute path or a dotted filesystem reference gets
# redacted before a converter message reaches the wire, along with exception
# class names: converter exceptions can embed temp paths, host paths, internal
# file names, and internal class names.
_PATH_LIKE = re.compile(r"(?:[A-Za-z]:)?(?:/|\\)[^\s\"'<>]+")
_EXC_TOKEN = re.compile(r"\b[A-Za-z_][A-Za-z0-9_]*(?:Error|Exception)\b")


def _clean_message(message: str) -> str:
    cleaned = _PATH_LIKE.sub("[path]", (message or ""))
    cleaned = _EXC_TOKEN.sub("[converter]", cleaned)
    cleaned = cleaned.strip()
    return cleaned[:300] if cleaned else "converter failed"


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
    """Convert raw file bytes to markdown. Raises ConversionError on failure.

    Runs inside the bounded pool with a hard timeout so a pathological
    document cannot hold a thread (or the caller) forever.
    """
    if not data:
        raise ConversionError(400, "bad_request", "empty request body")

    ext = _resolve_extension(filename, content_type)
    if not ext:
        raise ConversionError(
            422,
            "unsupported_format",
            "no filename extension and no content type to infer one from",
        )

    future = _convert_pool.submit(_convert_stream, data, ext)
    try:
        return future.result(timeout=CONVERT_TIMEOUT_SECONDS)
    except FutureTimeout:
        future.cancel()
        raise ConversionError(
            422,
            "conversion_failed",
            f"conversion timed out after {int(CONVERT_TIMEOUT_SECONDS)}s",
        ) from None


def _convert_stream(data: bytes, ext: str) -> str:
    """Run markitdown on an in-memory stream; no temp files, no host paths."""

    def run() -> str:
        result = _converter.convert_stream(io.BytesIO(data), file_extension=ext)
        text = result.text_content or ""
        if not text.strip():
            raise ConversionError(
                422, "empty_result", "converter produced no extractable text"
            )
        return text

    try:
        return run()
    except ConversionError:
        raise
    except mdx.UnsupportedFormatException as exc:
        raise ConversionError(
            422, "unsupported_format", _clean_message(str(exc)) or f"no converter for {ext}"
        ) from exc
    except (mdx.FileConversionException, mdx.MissingDependencyException) as exc:
        raise ConversionError(
            422, "conversion_failed", _clean_message(str(exc)) or f"converter failed for {ext}"
        ) from exc
    except Exception as exc:  # noqa: BLE001 loud-fail boundary
        raise ConversionError(500, "conversion_failed", _clean_message(f"{type(exc).__name__}: {exc}")) from exc


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
