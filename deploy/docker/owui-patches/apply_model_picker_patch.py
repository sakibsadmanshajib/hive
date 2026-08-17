#!/usr/bin/env python3
"""Build-time splice: filter Hive's non-chat aliases out of the listing Open
WebUI's chat model picker reads (issue #792).

The anchor is inside `main.py`'s `/api/models` handler, so the filter runs on
that response and on nothing else. It is inserted after `get_filtered_models`
rather than replacing it, and it is inserted UNCONDITIONALLY: the Hive filter
must not inherit upstream's access-control gating, which
`BYPASS_MODEL_ACCESS_CONTROL: "true"` disables for every role on this
deployment and which exempts administrators regardless. That gating is exactly
why #776's `access_control` mechanism shipped inert, and `assert_unconditional`
below is what stops this one from repeating it.

Asserts its own effect and fails the build otherwise, the same posture as this
Dockerfile's other patches: a future open-webui digest bump whose `/api/models`
handler shifted breaks the build loudly rather than silently restoring the
three aliases to the dropdown.

The transform lives in `patch()` so `scripts/test_owui_model_picker_filter.py`
can run the real thing against a checked-in excerpt of the pinned image's own
`main.py`. PR CI never builds this image, so without that the patch would be
verified only at deploy time.
"""

import ast
import pathlib
import re
import sys

TARGET = pathlib.Path("/app/backend/open_webui/main.py")

SIGNATURE = "async def get_models(request: Request, refresh: bool = False, user=Depends(get_verified_user)):\n"
ANCHOR = "    models = await get_filtered_models(models, user)\n"
RETURN = "    return {'data': models}\n"
CALL = "    models = _hive_filter_models(models, os.environ)\n"

INSERT = (
    """    # hive #792: the three non-chat catalog aliases (embeddings, STT, TTS)
    # must not appear in the chat model picker, while the gateway keeps
    # serving all six on /v1/models for direct API clients. This runs on the
    # response only -- request.app.state.MODELS is untouched, so every
    # invocation path (chat, document RAG embeddings, text-to-speech) is
    # unaffected. Unconditional by design: the access-control filter above is
    # disabled deployment-wide by BYPASS_MODEL_ACCESS_CONTROL and is
    # admin-exempt regardless, and every tenant owner here is an admin.
    from open_webui.utils.hive_model_picker import filter_models as _hive_filter_models

"""
    + CALL
)


def handler_body(text: str) -> str:
    """Return the source of main.py's /api/models handler."""
    assert text.count(SIGNATURE) == 1, (
        "the /api/models handler is not defined exactly once with the expected "
        "signature -- upstream open-webui source shifted, patch needs updating"
    )
    start = text.index(SIGNATURE) + len(SIGNATURE)
    next_top_level = re.search(r"\n@|\n\S", text[start:])
    end = start + next_top_level.start() if next_top_level else len(text)
    return text[start:end]


def assert_unconditional(body: str) -> None:
    """Fail unless the Hive filter runs on every request to this handler.

    This is the guard #776 did not have. Its mechanism was real code that a
    deployment flag switched off, and every test it shipped with still passed.
    Any future edit that puts the call behind a flag, a role check, or an
    access-control branch trips this, because the statement would no longer sit
    at the handler's own indentation level.
    """
    for line in body.splitlines(keepends=True):
        if line.lstrip().startswith("models = _hive_filter_models("):
            assert line == CALL.rstrip("\n") + "\n" or line == CALL, (
                "the hive picker filter is indented deeper than the handler "
                "body, so something now gates it. It must run for every "
                "request: BYPASS_MODEL_ACCESS_CONTROL and "
                "BYPASS_ADMIN_ACCESS_CONTROL between them disable every "
                "conditional Open WebUI offers here (issue #792)."
            )
            return
    raise AssertionError("the hive picker filter call is not in the /api/models handler")


def patch(text: str) -> str:
    """Return main.py with the picker filter spliced into /api/models."""
    body = handler_body(text)

    assert text.count(ANCHOR) == 1, (
        "the 'models = await get_filtered_models(models, user)' anchor is not "
        "present exactly once -- upstream open-webui source shifted, patch "
        "needs updating"
    )
    assert ANCHOR in body, (
        "the get_filtered_models anchor is not inside the /api/models "
        "handler's own body -- upstream open-webui source shifted, patch needs "
        "updating"
    )
    assert RETURN in body, (
        "the /api/models handler no longer returns {'data': models} -- the "
        "filter would run on a value the response does not use, patch needs "
        "updating"
    )
    assert "@app.get('/api/models')\n" in text, (
        "main.py no longer mounts GET /api/models -- patch needs updating"
    )
    assert re.search(r"^import os$", text, re.MULTILINE), (
        "main.py no longer imports os -- patch needs updating"
    )

    patched = text.replace(ANCHOR, ANCHOR + INSERT, 1)
    patched_body = handler_body(patched)
    assert_unconditional(patched_body)
    assert patched_body.index(CALL) > patched_body.index(ANCHOR), (
        "the filter must run after upstream's own filtering, not before"
    )
    assert patched_body.index(CALL) < patched_body.index(RETURN), (
        "the filter must run before the handler returns, or the response is "
        "unchanged -- which is precisely how #776 shipped inert"
    )
    ast.parse(patched)  # never write a main.py that cannot be imported
    return patched


if __name__ == "__main__":
    TARGET.write_text(patch(TARGET.read_text()))
    sys.stdout.write("hive #792: /api/models picker filter spliced into main.py\n")
