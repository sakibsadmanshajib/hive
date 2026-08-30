#!/usr/bin/env python3
"""Build-time fix for the background-task authentication gap (issue #1567).

WHAT WAS BROKEN
---------------
Every `/api/task/*` background completion reached edge-api under the static Open
WebUI shim key with no `__metadata.upstream_auth`, and edge-api's `OWUIUnwrap`
middleware treats `/v1/chat/completions` as unconditionally requiring a per-user
token, so it failed closed with 401 UNAUTHENTICATED. Confirmed on the running
demo box by second-for-second log correlation across two containers: seven
`Query generation failed` lines in the chat container, seven
`owui shim request missing upstream_auth metadata ... rejected=true` lines in
edge-api at the identical seconds, and no unpaired occurrence on either side.

The cause is which filter mechanism each path uses.
`deploy/docker/pipelines/hive_jwt_forward.py`, the thing that injects the
credential, is a NATIVE Functions Filter, and Open WebUI runs that chain only
from `process_chat_payload` and `process_filter_functions`, the main chat path.
`routers/tasks.py` runs `process_pipeline_inlet_filter`, the legacy external
Pipelines mechanism, and then calls `utils/chat.py::generate_chat_completion`
directly. The Filter is never invoked there. Deterministic, model independent,
and unrelated to the free-pool 429 rate in #1564.

WHERE THE FIX GOES, and why not the other candidates
----------------------------------------------------
At the tail of `utils/chat.py::generate_chat_completion`, on the arm that
dispatches to `routers/openai.py`. That is the single point every
server-originated OpenAI-compatible chat completion in this container passes
through: the main chat path, all eight task handlers, tool function calling
(`utils/middleware.py`), context compaction, and the memory subsystem. One seam,
not eight call sites, and no ninth task type can be added upstream without
inheriting the fix.

Not `routers/tasks.py`: eight identical edits, and it would leave the three
non-task callers above still broken.

Not `routers/pipelines.py::process_pipeline_inlet_filter`: one edit and it
covers the eight task handlers, but the three non-task callers do not go through
it either, so it is a smaller seam wearing the same cost.

Not `routers/openai.py::get_headers_and_cookies`: that helper also serves
embeddings, text to speech and model listing, which authenticate as the shim
account BY DESIGN (`requiresPerUserAuth` in
apps/edge-api/internal/auth/owui_unwrap.go names exactly two families and those
are not among them). Injecting there would change which principal those bill to,
which is a widening rather than a fix.

The three arms this deliberately does NOT reach are the ones where forwarding
the credential would be wrong: Ollama, pipe/function models, and direct
connections. A direct connection is relayed to the user's own browser over
socket.io, so putting a server-resolved OAuth token into that payload would put
it in page JavaScript, which is a downgrade and not a fix (same reasoning as the
agent-proxy note in Dockerfile.open-webui).

WHAT IS NOT DONE, said plainly
------------------------------
Nothing about the authentication boundary moves. `requiresPerUserAuth` is
untouched, the shim key gains no new authority, and edge-api has no change in
this pull request at all. The alternative fix, letting the shim key satisfy
`/v1/chat/completions` on its own, would make the error disappear by making
every background completion bill and audit against the shim's account, and this
repository has already shipped one live admin bypass by relaxing an auth check
(#1511, fixed in 9916c6ec5).

Not a vendored edit, because a vendored edit would ship nothing:
Dockerfile.open-webui builds only the frontend from vendor/open-webui and keeps
the pinned upstream image for the Python backend.

Fail-loud posture, identical to the sibling patches: exact-literal anchors with
expected occurrence counts, ast.parse after every rewrite, marker totals
asserted at the end and again by the Dockerfile drift guard, idempotent
early-exit once applied. HIVE_OWUI_BACKEND_DIR overrides the target directory
for scripts/test_owui_task_upstream_auth.py.
"""

import ast
import os
import pathlib

BACKEND = pathlib.Path(os.environ.get("HIVE_OWUI_BACKEND_DIR", "/app/backend/open_webui"))

MARKER = "# hive (#1567)"
TARGET = "utils/chat.py"

EDITS = [
    # 1. The import. hive_upstream_auth resolves its own Open WebUI dependency
    #    lazily inside a function, so importing it at module scope here closes
    #    no cycle even though utils/middleware.py imports this module.
    (
        "from open_webui.utils.models import check_model_access, get_all_models\n",
        f"{MARKER}: the task path never runs the native Functions Filter chain,\n"
        "# so the signed-in user's credential is attached at the dispatch seam\n"
        "# instead. See deploy/docker/owui-patches/hive_upstream_auth.py.\n"
        "from open_webui.utils.hive_upstream_auth import (\n"
        "    attach_upstream_auth as hive_attach_upstream_auth,\n"
        ")\n"
        "from open_webui.utils.models import check_model_access, get_all_models\n",
        1,
    ),
    # 2. The dispatch seam. Deliberately inside the final `else`, after the
    #    Ollama, pipe and direct-connection arms have already returned, so the
    #    credential travels only to the OpenAI-compatible upstream.
    #
    #    The call is a no-op when the payload already carries a token, which is
    #    the main chat path: the Filter injected one earlier in the turn, and
    #    resolving the OAuth session a second time would cost a database read
    #    per turn for a value already in hand.
    (
        "        else:\n"
        "            return await generate_openai_chat_completion(\n"
        "                request=request,\n"
        "                form_data=form_data,\n"
        "                user=user,\n"
        "            )\n",
        "        else:\n"
        f"            {MARKER}: carry the caller's own token to the gateway, so a\n"
        "            # background task completion is billed and audited to the user\n"
        "            # it was performed for rather than rejected under the shim key\n"
        "            form_data = await hive_attach_upstream_auth(request, form_data, user)\n"
        "            return await generate_openai_chat_completion(\n"
        "                request=request,\n"
        "                form_data=form_data,\n"
        "                user=user,\n"
        "            )\n",
        1,
    ),
]

EXPECTED_MARKERS = 2


def main():
    target = BACKEND / TARGET

    if target.read_text().count(MARKER) == EXPECTED_MARKERS:
        print("apply_task_upstream_auth_patch: already applied")
        return

    for old, new, expected in EDITS:
        text = target.read_text()
        found = text.count(old)
        assert found == expected, (
            f"{TARGET}: anchor found {found} times, expected {expected}; upstream "
            "open-webui source shifted, patch needs updating. Anchor head: "
            f"{old[:90]!r}"
        )
        patched = text.replace(old, new)
        ast.parse(patched)  # fails the build if a rewrite produced invalid Python
        target.write_text(patched)

    final = target.read_text()

    assert final.count(MARKER) == EXPECTED_MARKERS, (
        f"expected exactly {EXPECTED_MARKERS} {MARKER} markers in {TARGET} after "
        f"patching, found {final.count(MARKER)}"
    )

    # The seam has to sit on the OpenAI arm, not merely somewhere in the file. A
    # marker count cannot see placement, and an injection that landed above the
    # Ollama or direct-connection branches would forward the user's token to a
    # payload relayed into their own browser.
    assert (
        "form_data = await hive_attach_upstream_auth(request, form_data, user)\n"
        "            return await generate_openai_chat_completion(" in final
    ), (
        "the credential injection is no longer immediately above the "
        "generate_openai_chat_completion dispatch"
    )

    # And it must be the only dispatch to that forwarder, so no second arm can
    # reach the gateway without it.
    assert final.count("await generate_openai_chat_completion(") == 1, (
        "utils/chat.py gained a second generate_openai_chat_completion dispatch; "
        "it would reach the gateway with no per-user credential"
    )

    print("apply_task_upstream_auth_patch: attached the per-user credential at the dispatch seam")


if __name__ == "__main__":
    main()
