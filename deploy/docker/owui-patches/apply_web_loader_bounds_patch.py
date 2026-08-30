"""Build-time splice: bound the web-search page loader's response size.

Issue #298 PR review (MEDIUM): SafeWebBaseLoader._fetch() reads an entire
response body into memory unconditionally via `await response.text()`, with
no cap. An authenticated user can steer a slow or very large page into the
top 5 web-search results for a chosen query. WEB_LOADER_TIMEOUT (set in
docker-compose.yml, see the comment there for the measured value) already
bounds the wall-clock side per fetch attempt; this patch bounds the memory
side, which a timeout alone does not cover -- a large body served quickly
still buffers in full before any timeout would fire.

10 MiB is a hard ceiling chosen to sit far above any legitimate single page's
extracted-text needs (real long-form articles and docs run to a few hundred
KB of HTML at most) while keeping worst-case memory for one fetch small and
fixed, not open-ended. It is not derived from the same latency measurement as
WEB_LOADER_TIMEOUT: one bounds time, the other bounds size, and a page can
independently be slow, large, both, or neither.

Streams via `response.content.iter_chunked()` and aborts the moment the
running total crosses the cap, rather than trusting a self-reported
Content-Length header, since the threat model here is an adversarial or
adversarially-selected page that has no reason to report its size honestly.

Raising ValueError (not aiohttp.ClientConnectionError) means an oversized
response fails this fetch attempt immediately instead of being retried three
times against the same oversized page.

Asserts the exact pinned literal so a future digest bump whose loader shifted
breaks the build loudly instead of silently reverting to the unbounded read.
"""

import ast
import pathlib

TARGET = pathlib.Path("/app/backend/open_webui/retrieval/web/utils.py")

OLD = """                        return await response.text()
"""

NEW = """                        # hive: stream with a hard byte cap instead of
                        # buffering the whole response unconditionally. See
                        # apply_web_loader_bounds_patch.py for the reasoning.
                        max_bytes = 10 * 1024 * 1024  # 10 MiB
                        body = bytearray()
                        async for chunk in response.content.iter_chunked(65536):
                            body.extend(chunk)
                            if len(body) > max_bytes:
                                raise ValueError(
                                    f'response for {url} exceeded {max_bytes} bytes, aborting fetch'
                                )
                        return body.decode(response.get_encoding() or 'utf-8', errors='replace')
"""

text = TARGET.read_text()

assert text.count(OLD) == 1, (
    "the unbounded response.text() read is not present exactly once -- "
    "upstream open-webui source shifted, patch needs updating"
)

patched = text.replace(OLD, NEW, 1)
ast.parse(patched)  # never write an engine module that cannot be imported
TARGET.write_text(patched)
