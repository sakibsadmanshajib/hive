#!/usr/bin/env python3
"""Build-time strip of Hive's internal `__metadata` carrier at the connection
boundary (issue #1578).

WHAT WAS BROKEN
---------------
`__metadata` is a Hive invention, not an Open WebUI or OpenAI field. It exists
so the signed-in user's credential can reach edge-api's `OWUIUnwrap` middleware
through a request body, because Open WebUI puts one static shim key on
Authorization and offers no per-user header. Two things write it:
`deploy/docker/pipelines/hive_jwt_forward.py` on the main chat path and
`deploy/docker/owui-patches/hive_upstream_auth.py` at the task dispatch seam
(#1567).

Nothing took it back off. `routers/openai.py::generate_chat_completion` pops
the upstream `metadata` key and leaves `__metadata` in the forwarded payload,
so it travels to whichever OpenAI-compatible connection owns the resolved
model. Today that is always Hive's own gateway, which is the only reason no
credential has left. One administrator action changes that: point the external
task model (`task.model.external`) at a model served by a second, vendor
connection and every conversation ships `upstream_auth: Bearer <the user's
Supabase token>` to that vendor. No user action, no second misconfiguration.

WHERE THE FIX GOES, and why not the writers
-------------------------------------------
At the connection boundary, on the last line before each request is
serialised, once the destination is known. A guard at the two writers would
have to be remembered by a third writer, and the carrier is a plain dict key
that anything can set. The boundary is the only place that knows where the
request is going.

`routers/openai.py` has five requests that leave with a body, and all five are
patched: `generate_chat_completion`, `embeddings`, `responses`, `speech` and
the (default-disabled) `proxy` passthrough. That count is asserted below, so a
sixth added upstream fails this build rather than quietly becoming a hole.

Two of them serialised their body BEFORE resolving the connection, so those
two lines move down rather than gaining a call. The move is the fix, not
housekeeping: a body serialised before the destination is known cannot be
sanitised against it.

WHAT IS NOT DONE, said plainly
------------------------------
Nothing about the authentication boundary moves. `requiresPerUserAuth` in
apps/edge-api/internal/auth/owui_unwrap.go is untouched, the shim key gains
nothing, edge-api has no change in this pull request, and the Hive arm's
behaviour is byte-for-byte what it was: the carrier is removed only for a
destination this deployment does not declare as its own gateway.

An external task model pointed at a vendor connection is still permitted. It is
a legitimate configuration (a tenant may want a cheap vendor model summarising
titles) and refusing it at configuration time would cover exactly one of the
ways a vendor connection gets used, leaving the composer's own model picker
untouched. Sanitising at the boundary covers all of them. Recorded here because
issue #1578 asks for the decision to be recorded, not merely made.

Fail-loud posture, identical to the sibling patches: exact-literal anchors with
expected occurrence counts, ast.parse after every rewrite, marker totals
asserted at the end and again by the Dockerfile drift guard, idempotent
early-exit once applied. HIVE_OWUI_BACKEND_DIR overrides the target directory
for scripts/test_owui_internal_metadata_boundary.py.
"""

import ast
import os
import pathlib

BACKEND = pathlib.Path(os.environ.get('HIVE_OWUI_BACKEND_DIR', '/app/backend/open_webui'))

MARKER = '# hive (#1578)'
TARGET = 'routers/openai.py'

EDITS = [
    # 1. The import. The helper reads only os.environ and the standard library,
    #    so it closes no cycle wherever it is imported from.
    (
        'from open_webui.utils.headers import get_custom_headers, include_user_info_headers\n',
        'from open_webui.utils.headers import get_custom_headers, include_user_info_headers\n'
        f'{MARKER}: `__metadata` is a Hive-internal carrier (the signed-in\n'
        "# user's credential, among other things) and must not cross into a\n"
        '# third-party connection. See owui-patches/hive_internal_metadata.py.\n'
        'from open_webui.utils.hive_internal_metadata import (\n'
        '    strip_internal_metadata as hive_strip_internal_metadata,\n'
        '    strip_internal_metadata_body as hive_strip_internal_metadata_body,\n'
        ')\n',
        1,
    ),
    # 2. speech(). The body is the browser's, forwarded verbatim, and `idx` is
    #    resolved by looking up api.openai.com in the connection list, so the
    #    destination is known before the body is read. Sanitised BEFORE the
    #    sha256 that names the cache entry, so the cache key describes the body
    #    that actually leaves rather than one that never does.
    (
        '        body = await request.body()\n'
        '        name = hashlib.sha256(body).hexdigest()\n',
        '        body = await request.body()\n'
        f'        {MARKER}: before the hash, so the cache key names the body\n'
        '        # that actually leaves. api_base_urls[idx] is this request\'s\n'
        '        # destination, resolved on the line above.\n'
        '        body = hive_strip_internal_metadata_body(body, api_base_urls[idx])\n'
        '        name = hashlib.sha256(body).hexdigest()\n',
        1,
    ),
    # 3. generate_chat_completion(). The path this issue was filed for. Placed
    #    on the last line before serialisation, so it is downstream of the
    #    Azure and Responses payload conversions and of every other rewrite
    #    this function performs: whatever any of them do to `__metadata`, the
    #    boundary has the last word.
    (
        '    payload = json.dumps(payload)\n',
        f'    {MARKER}: last word on the carrier, after every payload rewrite\n'
        '    # above and immediately before this leaves the container.\n'
        '    payload = hive_strip_internal_metadata(payload, url)\n'
        '    payload = json.dumps(payload)\n',
        1,
    ),
    # 4a. embeddings(). The body was serialised at the top of the function,
    #     before the connection was resolved. Removed here and rebuilt in 4b.
    (
        '    idx = 0\n'
        '    # Prepare payload/body\n'
        '    body = json.dumps(form_data)\n'
        '    # Find correct backend url/key based on model\n'
        '    model_id = form_data.get(\'model\')\n',
        '    idx = 0\n'
        f'    {MARKER}: the body is serialised after the connection is known,\n'
        '    # further down, because it cannot be sanitised against a\n'
        '    # destination that has not been resolved yet.\n'
        '    # Find correct backend url/key based on model\n'
        '    model_id = form_data.get(\'model\')\n',
        1,
    ),
    # 4b. embeddings(), the rebuild.
    (
        '    url, key, api_config = await get_openai_connection(idx)\n'
        '\n'
        '    r = None\n'
        '    streaming = False\n'
        '\n'
        '    headers, cookies = await get_headers_and_cookies(request, url, key, api_config, user=user)\n',
        '    url, key, api_config = await get_openai_connection(idx)\n'
        '\n'
        '    body = json.dumps(hive_strip_internal_metadata(form_data, url))\n'
        '\n'
        '    r = None\n'
        '    streaming = False\n'
        '\n'
        '    headers, cookies = await get_headers_and_cookies(request, url, key, api_config, user=user)\n',
        1,
    ),
    # 5a. responses(). Same shape as embeddings: serialised before the
    #     connection was resolved.
    (
        '    body = json.dumps(payload)\n'
        '\n'
        '    if model_id:\n',
        f'    {MARKER}: serialised after the connection is known, below.\n'
        '    if model_id:\n',
        1,
    ),
    # 5b. responses(), the rebuild.
    (
        '    url, key, api_config = await get_openai_connection(idx)\n'
        '\n'
        '    r = None\n'
        '    streaming = False\n'
        '\n'
        '    try:\n'
        '        headers, cookies = await get_headers_and_cookies(request, url, key, api_config, user=user)\n',
        '    url, key, api_config = await get_openai_connection(idx)\n'
        '\n'
        '    body = json.dumps(hive_strip_internal_metadata(payload, url))\n'
        '\n'
        '    r = None\n'
        '    streaming = False\n'
        '\n'
        '    try:\n'
        '        headers, cookies = await get_headers_and_cookies(request, url, key, api_config, user=user)\n',
        1,
    ),
    # 6. proxy(). Default disabled (ENABLE_OPENAI_API_PASSTHROUGH), and its
    #    body is the caller's own bytes rather than anything this container
    #    injected, but it forwards to the same administrator-configured
    #    connections and the claim in the helper's docstring is that NOTHING
    #    internal to Hive crosses to a vendor. Placed above the Azure branch,
    #    which re-parses `body`, so that branch inherits the sanitised bytes.
    (
        '    url, key, api_config = await get_openai_connection(idx)\n'
        '    base_url = url\n',
        '    url, key, api_config = await get_openai_connection(idx)\n'
        '    base_url = url\n'
        '\n'
        f'    {MARKER}: above the Azure rewrite below, which re-parses `body`.\n'
        '    body = hive_strip_internal_metadata_body(body, base_url)\n',
        1,
    ),
]

EXPECTED_MARKERS = 6

# Every call into aiohttp from this module that carries a request body. The
# count is the load-bearing part: it is what turns "all five outbound bodies
# are sanitised" into a claim the build re-checks, instead of a claim that was
# true on the day it was written. A digest bump that adds a sixth fails here.
BODY_ARGUMENTS = ('data=payload,', 'data=body,')
EXPECTED_BODY_ARGUMENTS = 5


def main():
    target = BACKEND / TARGET

    if target.read_text().count(MARKER) == EXPECTED_MARKERS:
        print('apply_internal_metadata_boundary_1578_patch: already applied')
        return

    # Every edit is applied in memory and the file is written ONCE, at the end,
    # so a drifted anchor cannot leave a half-patched file behind for a re-run
    # to double-apply. Same reasoning as apply_task_upstream_auth_patch.py.
    final = target.read_text()

    outbound = sum(final.count(argument) for argument in BODY_ARGUMENTS)
    assert outbound == EXPECTED_BODY_ARGUMENTS, (
        f'{TARGET} makes {outbound} requests carrying a body, expected '
        f'{EXPECTED_BODY_ARGUMENTS}. Upstream added or removed one; every one of '
        'them has to be sanitised or the boundary has a hole. Patch needs updating.'
    )

    for old, new, expected in EDITS:
        found = final.count(old)
        assert found == expected, (
            f'{TARGET}: anchor found {found} times, expected {expected}; upstream '
            'open-webui source shifted, patch needs updating. Anchor head: '
            f'{old[:90]!r}'
        )
        final = final.replace(old, new)

    ast.parse(final)  # fails the build if a rewrite produced invalid Python

    assert final.count(MARKER) == EXPECTED_MARKERS, (
        f'expected exactly {EXPECTED_MARKERS} {MARKER} markers in {TARGET} after '
        f'patching, found {final.count(MARKER)}'
    )

    # Placement, which a marker count cannot see. The strip has to be the line
    # immediately before the payload is frozen into JSON; anywhere earlier and a
    # later rewrite could reintroduce the carrier after the boundary had spoken.
    assert (
        '    payload = hive_strip_internal_metadata(payload, url)\n    payload = json.dumps(payload)\n' in final
    ), 'the chat-completions strip is no longer immediately above the json.dumps that freezes the payload'

    # Neither body may be serialised before its connection is resolved again.
    assert 'body = json.dumps(form_data)\n' not in final, (
        'embeddings still serialises its body before get_openai_connection, so the '
        'sanitiser would be comparing against a destination it does not know'
    )
    assert 'body = json.dumps(payload)\n' not in final, (
        'responses still serialises its body before get_openai_connection, so the '
        'sanitiser would be comparing against a destination it does not know'
    )

    # Five outbound bodies, five sanitiser call sites. Counted as a pair so
    # neither can drift without the other. The trailing parenthesis is what
    # keeps the two import aliases out of the count.
    sanitiser_calls = final.count('hive_strip_internal_metadata(') + final.count(
        'hive_strip_internal_metadata_body('
    )
    assert sanitiser_calls == EXPECTED_BODY_ARGUMENTS, (
        f'{TARGET} has {sanitiser_calls} sanitiser call sites for '
        f'{EXPECTED_BODY_ARGUMENTS} outbound bodies; every body that leaves this '
        'module must pass through the boundary'
    )

    # Everything above ran against the in-memory result, so nothing is written
    # until the whole rewrite is known to be good.
    target.write_text(final)

    print('apply_internal_metadata_boundary_1578_patch: the internal carrier is stripped at the connection boundary')


if __name__ == '__main__':
    main()
