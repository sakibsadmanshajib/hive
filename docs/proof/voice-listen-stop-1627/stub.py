#!/usr/bin/env python3
"""Minimal OpenAI-compatible stub for the issue #1627 voice proof.

It exists to answer the four calls the chat front end makes during a voice
turn and, more importantly, to write down exactly when each one arrived. The
transcription log is the flood evidence: one utterance must produce one
POST /v1/audio/transcriptions and nothing while the room is quiet.

No credentials, no upstream. Everything it returns is canned.
"""

import json
import struct
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

START = time.time()
LOG_PATH = '/out/stub-requests.log'
LOCK = threading.Lock()

TRANSCRIPT = 'This is the voice mode test for issue sixteen twenty seven.'


def log(line: str, at: float | None = None) -> None:
    stamped = f'[{(time.time() - START) if at is None else at:8.3f}s] {line}'
    with LOCK:
        print(stamped, flush=True)
        with open(LOG_PATH, 'a', encoding='utf-8') as handle:
            handle.write(stamped + '\n')


def silent_wav(seconds: float = 0.4, rate: int = 24000) -> bytes:
    frames = int(seconds * rate)
    data = b'\x00\x00' * frames
    header = b'RIFF' + struct.pack('<I', 36 + len(data)) + b'WAVE'
    header += b'fmt ' + struct.pack('<IHHIIHH', 16, 1, 1, rate, rate * 2, 2, 16)
    header += b'data' + struct.pack('<I', len(data))
    return header + data


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *args):  # noqa: D102 - silence the default access log
        return

    def _send(self, status: int, body: bytes, content_type: str) -> None:
        self.send_response(status)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _json(self, payload, status: int = 200) -> None:
        self._send(status, json.dumps(payload).encode(), 'application/json')

    def do_GET(self):
        if self.path.rstrip('/').endswith('/models'):
            log('GET  /v1/models')
            self._json(
                {
                    'object': 'list',
                    'data': [
                        {
                            'id': 'hive-default',
                            'object': 'model',
                            'created': 0,
                            'owned_by': 'hive'
                        }
                    ]
                }
            )
            return
        log(f'GET  {self.path} (unhandled)')
        self._json({'error': 'not found'}, 404)

    def do_POST(self):
        # Stamped before the body is read, not after: these timestamps are the
        # evidence for when a request arrived, and reading a megabyte of audio
        # off the socket first would fold the upload into the arrival time.
        arrived = time.time() - START
        length = int(self.headers.get('Content-Length') or 0)
        body = self.rfile.read(length) if length else b''

        if self.path.endswith('/audio/transcriptions'):
            log(f'POST /v1/audio/transcriptions  bytes={len(body)}', at=arrived)
            self._json({'text': TRANSCRIPT})
            return

        if self.path.endswith('/audio/speech'):
            log(f'POST /v1/audio/speech  bytes={len(body)}', at=arrived)
            self._send(200, silent_wav(), 'audio/wav')
            return

        if self.path.endswith('/chat/completions'):
            # Parsed rather than grepped: a substring search matches a nested
            # `stream` on some future request shape and silently sends the
            # wrong response kind, which would change what the capture proves.
            try:
                request = json.loads(body)
            except (json.JSONDecodeError, UnicodeDecodeError):
                log('POST /v1/chat/completions  invalid json', at=arrived)
                self._json({'error': 'invalid json'}, 400)
                return
            streaming = isinstance(request, dict) and request.get('stream') is True
            log(f'POST /v1/chat/completions  stream={streaming}', at=arrived)
            if not streaming:
                self._json(
                    {
                        'id': 'stub',
                        'object': 'chat.completion',
                        'created': int(time.time()),
                        'model': 'hive-default',
                        'choices': [
                            {
                                'index': 0,
                                'message': {'role': 'assistant', 'content': 'Heard you.'},
                                'finish_reason': 'stop'
                            }
                        ]
                    }
                )
                return

            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Cache-Control', 'no-cache')
            self.send_header('Connection', 'close')
            self.end_headers()
            for piece in ['Heard ', 'you. ', 'Stub ', 'reply.']:
                chunk = {
                    'id': 'stub',
                    'object': 'chat.completion.chunk',
                    'created': int(time.time()),
                    'model': 'hive-default',
                    'choices': [{'index': 0, 'delta': {'content': piece}}]
                }
                self.wfile.write(f'data: {json.dumps(chunk)}\n\n'.encode())
                self.wfile.flush()
                time.sleep(0.05)
            done = {
                'id': 'stub',
                'object': 'chat.completion.chunk',
                'created': int(time.time()),
                'model': 'hive-default',
                'choices': [{'index': 0, 'delta': {}, 'finish_reason': 'stop'}]
            }
            self.wfile.write(f'data: {json.dumps(done)}\n\n'.encode())
            self.wfile.write(b'data: [DONE]\n\n')
            self.wfile.flush()
            self.close_connection = True
            return

        log(f'POST {self.path} (unhandled)  bytes={len(body)}', at=arrived)
        self._json({'error': 'not found'}, 404)


if __name__ == '__main__':
    open(LOG_PATH, 'w', encoding='utf-8').close()
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8000
    log(f'stub listening on {port}')
    ThreadingHTTPServer(('0.0.0.0', port), Handler).serve_forever()
