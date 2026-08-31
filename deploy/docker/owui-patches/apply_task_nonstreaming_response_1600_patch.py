"""Build-time fix: a non-streaming request gets a streaming response (issue #1600).

Every task payload in `open_webui/routers/tasks.py` declares `'stream': False`
(seven of them: title, tags, follow-up, image prompt, web-search queries,
retrieval queries, autocompletion), and every in-process caller then subscripts
the result. `chat_web_search_handler` in `utils/middleware.py` does exactly that:

    response = res['choices'][0]['message']['content']

That subscript raised on the demo box, live, on every web-search send:

    ERROR | open_webui.utils.middleware:chat_web_search_handler:1359
          - 'StreamingResponse' object is not subscriptable

so no generated query was ever extracted, the handler's own `except` fell back
to `queries = [user_message]`, and SearXNG was asked the user's typed question
verbatim instead of a query written for retrieval. The search-query chip in the
chat UI showed the raw question, which is how it was noticed. Titles, tags and
follow-ups fail the same way one layer out: their route hands the browser an
SSE body, `generateTitle` in `src/lib/apis/index.ts` calls `res.json()` on it,
throws, and returns null, so the chat is silently left untitled. Issue #1567
set out to repair all of those and got them as far as authenticating; this is
what they hit next.

Where the shape actually drifts, traced rather than assumed:

  * `routers/tasks.py` sends `'stream': False` upstream. It is honest.
  * `utils/chat.py::generate_chat_completion` passes it through untouched.
  * `routers/openai.py` decides on the RESPONSE content type alone:
    `if 'text/event-stream' in r.headers.get('Content-Type', '')` returns a
    StreamingResponse, whatever the request declared.
  * Hive's own edge-api is what sets that content type on a request that asked
    for no stream. `/v1/chat/completions` is registered as
    `jwtAwareChatHandler(chatDispatchHandler, inferenceHandler)`
    (apps/edge-api/cmd/server/main.go), so a JWT-authenticated call, which is
    what every OWUI task request now is once `hive_upstream_auth` attaches the
    signed-in user's token, goes to the session-chat dispatcher. That
    dispatcher forces streaming: `rewriteDispatchBody` sets
    `fields["stream"] = true` unconditionally and the handler always writes
    `Content-Type: text/event-stream`
    (apps/edge-api/internal/chat/dispatch.go). The API-key surface next to it
    honours `stream` correctly; only the JWT surface does not.

So the producer of the wrong shape is our own gateway, and the reason it is
patched HERE rather than there: making the session-chat dispatcher answer JSON
means restructuring the SSE relay that also carries credit settlement, on the
single hottest path in the product, where today's failure mode is confined to
task generation. That is a separate change with a much larger blast radius, and
it is recorded on the pull request as a follow-up rather than smuggled into a
query-generation fix.

What this patch does instead is honour the caller's own declaration at the
first point in the Python stack that can see the drift: one guard in
`routers/openai.py`, immediately before the branch that would return a
StreamingResponse, which collapses the SSE body into the completion object a
non-streaming caller asked for. One guard covers all seven task types plus
tool calling, memory and RAG query generation, rather than teaching each
caller to accept two shapes, which is what would hide the next drift.

It is not silent about it: the first collapse in a process logs a WARNING
naming the upstream that ignored `stream`, later ones log at debug so the
finding cannot bury the rest of the log, and a body that yields no content at
all logs a WARNING every time.

Applied here rather than in vendor/open-webui because the chat image builds
only the FRONTEND from the vendored tree and takes the backend from the pinned
upstream image (see Dockerfile.open-webui); a backend edit under vendor/ is
inert unless expressed here. Same fail-loud posture as the patches beside it:
exact-literal anchors with expected occurrence counts, ast.parse after the
rewrite, idempotent early-exit once applied. Behaviour is covered by
scripts/test_owui_task_nonstreaming_response.py, which also pins the digest of
the file as the backend image ships it, so a vendor bump cannot quietly turn
that test into evidence about a file the container does not run.
"""

import ast
import os
import pathlib

TARGET = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_OPENAI_PY",
        "/app/backend/open_webui/routers/openai.py",
    )
)

MARKER = "# hive (#1600)"

# Inserted once, immediately above the chat-completions route, so the route
# below can reach it.
DEF_ANCHOR = (
    "@router.post('/chat/completions')\n"
    "async def generate_chat_completion(\n"
    "    request: Request,\n"
    "    form_data: dict,\n"
    "    user=Depends(get_verified_user),\n"
    "):\n"
)

HELPER = '''_HIVE_SSE_COLLAPSE_WARNED = False


async def _hive_collapse_sse_completion(r, requested_model):
    """Read an SSE body into the non-streaming completion the caller asked for.

    ''' + MARKER + """: routers/tasks.py declares 'stream': False on every
    task payload and its callers subscript the result, so returning a
    StreamingResponse here breaks a contract they cannot see. Hive's edge-api
    answers the JWT session-chat surface with text/event-stream whatever the
    request declared (apps/edge-api/internal/chat/dispatch.go), which is what
    made this reachable. Collapsing at this boundary keeps the declared shape
    for every non-streaming caller at once.

    The frames are chat-completion shaped. A Responses-API connection
    (api_type 'responses') would yield no content and log the warning below,
    which is no worse than the TypeError that path raised before.
    \"\"\"
    global _HIVE_SSE_COLLAPSE_WARNED

    body = (await r.read()).decode('utf-8', errors='replace')

    content = ''
    usage = None
    finish_reason = None
    frame_id = None
    created = None
    model = None

    for line in body.splitlines():
        line = line.strip()
        if not line.startswith('data:'):
            continue
        data = line[len('data:'):].strip()
        if not data or data == '[DONE]':
            continue
        try:
            frame = json.loads(data)
        except Exception:
            continue
        if not isinstance(frame, dict):
            continue
        frame_id = frame.get('id') or frame_id
        created = frame.get('created') or created
        model = frame.get('model') or model
        if frame.get('usage'):
            usage = frame['usage']
        for choice in frame.get('choices') or []:
            if not isinstance(choice, dict):
                continue
            finish_reason = choice.get('finish_reason') or finish_reason
            part = choice.get('delta') or choice.get('message') or {}
            piece = part.get('content') if isinstance(part, dict) else None
            if isinstance(piece, str):
                content += piece

    if not content:
        log.warning(
            'hive: collapsed a streaming response for a non-streaming request '
            'to %s and found no content in %d bytes; the caller falls back',
            requested_model,
            len(body),
        )
    elif not _HIVE_SSE_COLLAPSE_WARNED:
        _HIVE_SSE_COLLAPSE_WARNED = True
        log.warning(
            'hive: upstream answered a non-streaming request to %s with '
            'text/event-stream; collapsing it into a completion object. '
            'Further collapses in this process log at debug level.',
            requested_model,
        )
    else:
        log.debug(
            'hive: collapsed another streaming response for a non-streaming '
            'request to %s',
            requested_model,
        )

    message = dict(role='assistant', content=content)
    only_choice = dict(index=0, message=message, finish_reason=finish_reason or 'stop')
    completion = dict(
        id=frame_id or '',
        object='chat.completion',
        model=model or requested_model,
        choices=[only_choice],
    )
    if created is not None:
        completion['created'] = created
    if usage:
        completion['usage'] = usage
    return completion


"""

SSE_ANCHOR = (
    "            streaming = True\n"
    "            return StreamingResponse(\n"
    "                stream_wrapper(r, content_handler=stream_chunks_handler),\n"
    "                status_code=r.status,\n"
    "                headers=_clean_proxy_headers(r.headers),\n"
    "            )\n"
)

SSE_GUARD = (
    "            " + MARKER + ": the caller declared no stream, so honour\n"
    "            # that instead of handing it a response it cannot read.\n"
    "            # `streaming` stays False on purpose: the `finally` below\n"
    "            # then closes the upstream response, which the body read\n"
    "            # inside the helper has already drained.\n"
    "            if not form_data.get('stream'):\n"
    "                return await _hive_collapse_sse_completion(r, requested_model)\n"
    "\n"
)

REPLACEMENTS = [(SSE_ANCHOR, SSE_GUARD + SSE_ANCHOR, 1)]

EXPECTED_MARKERS = 2  # helper docstring + the one call site


def main():
    text = TARGET.read_text()

    if MARKER in text:
        print("apply_task_nonstreaming_response_1600_patch: already applied")
        return

    assert text.count(DEF_ANCHOR) == 1, (
        "the chat-completions route is not declared exactly once -- "
        "upstream open-webui source shifted, patch needs updating"
    )
    patched = text.replace(DEF_ANCHOR, HELPER + DEF_ANCHOR, 1)

    for old, new, expected in REPLACEMENTS:
        n = patched.count(old)
        assert n == expected, (
            f"anchor found {n} times, expected {expected}; upstream "
            f"open-webui source shifted, patch needs updating. "
            f"Anchor head: {old[:90]!r}"
        )
        patched = patched.replace(old, new, expected)

    ast.parse(patched)  # fails the build if a rewrite produced invalid Python
    TARGET.write_text(patched)

    count = patched.count(MARKER)
    assert count == EXPECTED_MARKERS, (
        f"{TARGET}: {count} markers after patching, expected {EXPECTED_MARKERS}"
    )
    print(
        "apply_task_nonstreaming_response_1600_patch: a non-streaming request "
        "now gets a completion object, not a stream (#1600)"
    )


if __name__ == "__main__":
    main()
