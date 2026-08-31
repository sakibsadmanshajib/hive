"""Build-time fix: the chat error surface reads a key Hive never sends (issue #1569).

`chat_web_search_handler` in open_webui/utils/middleware.py pulls the failure
reason out of an upstream JSONResponse with:

    error_body.get('detail', 'Query generation failed')

Hive's edge-api `writeAuthError` emits the OpenAI envelope
({"error": {"code", "message", "type"}}), which carries no top-level 'detail'
key at all, so the lookup always misses and the hardcoded default is what the
user and the logs see on every failure, whatever the real cause was. It cost
real time on #1567: the toast read "Query generation failed" for what was
actually a 401 from an auth-filter gap, and the upstream message that said so
exactly was thrown away at this boundary.

Two sibling call sites in the same file have the same family of bug:

  * the image-prompt-generation handler, identical shape, one line down the
    file from the one above;
  * non_streaming_chat_response_handler's `error.get('detail', error)`, which
    also misses the 'message' key and falls back to dumping the raw error
    dict into chat content instead of a readable reason.

Two more readers in this vendored tree already handle both 'message' and
'detail' correctly (main.py's process_chat, events.py's
publish_model_provider_request_failed) and are left untouched: rewriting code
that already works only adds risk for no gain.

One shared helper fixes all three broken sites, rather than three separate
inline fixes, so a future fourth reader has something to call instead of
inventing its own `.get('detail', ...)` again. It accepts either the raw
top-level body ({"error": {...}}) or an already-unwrapped inner error object,
so every call site hands it whatever shape it already has.

Provider-blind sanitisation is unaffected: the message this surfaces is
whatever Hive's own error envelope already carries, sanitised by control-plane
and edge-api before emission. Nothing upstream-raw is newly exposed.

Applied here rather than in vendor/open-webui because the chat image builds
only the FRONTEND from the vendored tree and takes the backend from the pinned
upstream image (see Dockerfile.open-webui); a backend edit under vendor/ is
inert unless expressed here. Same fail-loud posture as the patches above:
exact-literal anchors with expected occurrence counts, ast.parse after the
rewrite, idempotent early-exit once applied. Behaviour is covered by
scripts/test_owui_chat_error_detail.py.
"""

import ast
import os
import pathlib

TARGET = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_MIDDLEWARE_PY",
        "/app/backend/open_webui/utils/middleware.py",
    )
)

MARKER = "# hive (#1569)"

# Inserted once, immediately above chat_web_search_handler, so every call
# site below it in this file can reach it.
DEF_ANCHOR = (
    "async def chat_web_search_handler(request: Request, form_data: dict, "
    "extra_params: dict, user):\n"
)

HELPER = (
    "def _hive_extract_upstream_error_message(payload, default: str) -> str:\n"
    '    """Read a human-readable reason out of an upstream error payload.\n'
    "\n"
    "    " + MARKER + ": Hive's edge-api emits the OpenAI envelope,\n"
    '    {"error": {"code", "message", "type"}}, never a top-level FastAPI\n'
    '    "detail" key. `payload` may be that raw envelope or an already\n'
    "    unwrapped inner error object; both are accepted so every caller on\n"
    "    this path can hand this whatever shape it already has. `default` is\n"
    "    returned only when neither key carries anything usable, and must not\n"
    "    assert a cause this function does not know.\n"
    '    """\n'
    "    error = payload.get('error', payload) if isinstance(payload, dict) else payload\n"
    "    if isinstance(error, dict):\n"
    "        message = error.get('message') or error.get('detail')\n"
    "        if message:\n"
    "            return str(message)\n"
    "    elif isinstance(error, str) and error:\n"
    "        return error\n"
    "    return default\n"
    "\n"
    "\n"
)

REPLACEMENTS = [
    (
        "            try:\n"
        "                error_body = json.loads(res.body)\n"
        "                detail = error_body.get('detail', 'Query generation failed')\n"
        "            except Exception:\n"
        "                detail = 'Query generation failed'\n"
        "            raise Exception(detail)\n",
        "            try:\n"
        "                error_body = json.loads(res.body)\n"
        "                " + MARKER + ": error_body has no top-level 'detail'\n"
        "                # key; Hive's error is nested under 'error'. See the\n"
        "                # helper above chat_web_search_handler.\n"
        "                detail = _hive_extract_upstream_error_message(\n"
        "                    error_body, 'Query generation failed'\n"
        "                )\n"
        "            except Exception:\n"
        "                detail = 'Query generation failed'\n"
        "            raise Exception(detail)\n",
        1,
    ),
    (
        "                        error_body = json.loads(res.body)\n"
        "                        detail = error_body.get('detail', 'Image prompt generation failed')\n"
        "                    except Exception:\n"
        "                        detail = 'Image prompt generation failed'\n"
        "                    raise Exception(detail)\n",
        "                        error_body = json.loads(res.body)\n"
        "                        " + MARKER + ": same fix as the web-search\n"
        "                        # handler above.\n"
        "                        detail = _hive_extract_upstream_error_message(\n"
        "                            error_body, 'Image prompt generation failed'\n"
        "                        )\n"
        "                    except Exception:\n"
        "                        detail = 'Image prompt generation failed'\n"
        "                    raise Exception(detail)\n",
        1,
    ),
    (
        "                if isinstance(error, dict):\n"
        "                    error = error.get('detail', error)\n"
        "                else:\n"
        "                    error = str(error)\n",
        "                if isinstance(error, dict):\n"
        "                    " + MARKER + ": same as above, and the old\n"
        "                    # fallback dumped the raw error dict into chat\n"
        "                    # content instead of a readable reason.\n"
        "                    error = _hive_extract_upstream_error_message(\n"
        "                        error, 'Provider returned an error: ' + str(error)\n"
        "                    )\n"
        "                else:\n"
        "                    error = str(error)\n",
        1,
    ),
]

EXPECTED_MARKERS = 4  # helper docstring + 3 call sites


def main():
    text = TARGET.read_text()

    if MARKER in text:
        print("apply_chat_error_detail_1569_patch: already applied")
        return

    assert text.count(DEF_ANCHOR) == 1, (
        "chat_web_search_handler is not defined exactly once -- "
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
        "apply_chat_error_detail_1569_patch: chat error surface now reads "
        "Hive's OpenAI-shaped error message instead of a hardcoded string (#1569)"
    )


if __name__ == "__main__":
    main()
