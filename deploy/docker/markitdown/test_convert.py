"""Container test for the markitdown sidecar.

Runs inside the `python /app/test_convert.py` entrypoint. Stdlib unittest.
"""

import io
import json
import os
import threading
import unittest
import urllib.error
import urllib.request
import zipfile

import app as appmod

ConversionError = appmod.ConversionError

HERE = os.path.dirname(os.path.abspath(__file__))
with open(os.path.join(HERE, "testdata", "sample.pdf"), "rb") as f:
    SAMPLE_PDF = f.read()
with open(HERE + "/testdata/sample.docx", "rb") as f:
    SAMPLE_DOCX = f.read()


class ConvertBytesTests(unittest.TestCase):
    def test_pdf_fixture_converts(self):
        text = appmod.convert_bytes(SAMPLE_PDF, "sample.pdf", "application/pdf")
        self.assertIn("Hello from the synthetic PDF fixture.", text)

    def test_docx_fixture_converts(self):
        text = appmod.convert_bytes(SAMPLE_DOCX, "sample.docx", "")
        self.assertIn("Hello from the synthetic DOCX fixture.", text)

    def test_empty_body_rejected(self):
        with self.assertRaises(ConversionError) as cm:
            appmod.convert_bytes(b"", "a.txt", "text/plain")
        self.assertEqual(cm.exception.err_class, "bad_request")

    def test_no_extension_rejected(self):
        with self.assertRaises(ConversionError) as cm:
            appmod.convert_bytes(b"abc", "", "")
        self.assertEqual(cm.exception.err_class, "unsupported_format")

    def test_corrupt_docx_fails_loud(self):
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w") as z:
            z.writestr("[Content_Types].xml", "<Types></Types>")
        with self.assertRaises(ConversionError) as cm:
            appmod.convert_bytes(buf.getvalue(), "broken.docx", "")
        self.assertEqual(cm.exception.err_class, "conversion_failed")

    def test_garbage_bytes_echo_as_text(self):
        # markitdown's plain-text fallback converts unparseable payloads to
        # their literal bytes as text. That is a known semantic, documented
        # here; the edge-api extension allowlist keeps such uploads away.
        text = appmod.convert_bytes(b"plain notes", "notes.txt", "text/plain")
        self.assertIn("plain notes", text)


class _Client:
    def __init__(self, port):
        self.base = "http://127.0.0.1:" + str(port)

    def post(self, path, body, headers=None):
        req = urllib.request.Request(
            self.base + path, data=body, headers=headers or {}, method="POST"
        )
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                return resp.status, json.loads(resp.read().decode())
        except urllib.error.HTTPError as err:
            return err.code, json.loads(err.read().decode())

    def get(self, path):
        req = urllib.request.Request(self.base + path)
        try:
            with urllib.request.urlopen(req, timeout=10) as resp:
                return resp.status, json.loads(resp.read().decode())
        except urllib.error.HTTPError as err:
            return err.code, json.loads(err.read().decode())


def make_server():
    from http.server import ThreadingHTTPServer

    return ThreadingHTTPServer(("127.0.0.1", 0), appmod.Handler)


class HttpTests(unittest.TestCase):
    def setUp(self):
        self.server = make_server()
        self.port = self.server.server_address[1]
        t = threading.Thread(target=self.server.serve_forever, daemon=True)
        t.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()

    def test_healthz(self):
        status, body = _Client(self.port).get("/healthz")
        self.assertEqual(status, 200)
        self.assertEqual(body["status"], "ok")

    def test_unknown_path_404(self):
        status, body = _Client(self.port).get("/nope")
        self.assertEqual(status, 404)
        self.assertEqual(body["error"]["class"], "not_found")

    def test_convert_pdf_over_http(self):
        client = _Client(self.port)
        status, body = client.post(
            "/convert",
            SAMPLE_PDF,
            {"Content-Type": "application/pdf", "X-Filename": "report.pdf"},
        )
        self.assertEqual(status, 200)
        self.assertIn("Hello from the synthetic PDF fixture.", body["markdown"])

    def test_convert_docx_over_http(self):
        status, body = _Client(self.port).post(
            "/convert",
            SAMPLE_DOCX,
            {"X-Filename": "notes.docx"},
        )
        self.assertEqual(status, 200)
        self.assertIn("Hello from the synthetic DOCX fixture.", body["markdown"])

    def test_oversize_body_rejected_413(self):
        saved = appmod.MAX_UPLOAD_BYTES
        appmod.MAX_UPLOAD_BYTES = 1024
        try:
            client = _Client(self.port)
            status, body = client.post("/convert", b"x" * 4096, {})
            self.assertEqual(status, 413)
            self.assertEqual(body["error"]["class"], "payload_too_large")
        finally:
            appmod.MAX_UPLOAD_BYTES = restore = saved

    def test_corrupt_docx_loud_error_over_http(self):
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w") as z:
            z.writestr("[Content_Types].xml", "<Types></Types>")
        status, body = _Client(self.port).post(
            "/convert",
            buf.getvalue(),
            {"X-Filename": "broken.docx"},
        )
        self.assertEqual(status, 422)
        self.assertEqual(body["error"]["class"], "conversion_failed")

    def test_missing_content_length_rejected(self):
        import http.client

        conn = http.client.HTTPConnection("127.0.0.1", self.port, 10)
        conn.putrequest("POST", "/convert")
        conn.endheaders()
        resp = conn.getresponse()
        data = resp.read()
        conn.close()
        self.assertEqual(resp.status, 400)
        self.assertEqual(json.loads(data.decode())["error"]["class"], "bad_request")


if __name__ == "__main__":
    unittest.main(verbosity=2)
