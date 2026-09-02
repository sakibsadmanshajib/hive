#!/usr/bin/env python3
"""Build-time splice: put Hive's two web tools in front of every tool capable
model, and let Open WebUI's own native tool loop execute what the model calls
back (issue #1718).

Applied here rather than in vendor/open-webui because the chat image builds only
the FRONTEND from the vendored tree and takes the backend from the pinned
upstream image (see Dockerfile.open-webui), so a backend edit under vendor/ is
inert. Every assertion below is written to fail the IMAGE BUILD loudly if an
anchor moves, rather than to let the splice quietly not apply, which is the one
failure mode a patch of this shape has.

Three edits, all inside utils/middleware.py.

1. `process_chat_payload`, immediately above `if tools_dict:`. Replaces the
   resolved tool set with `hive_web_tools.select_tools(...)`, which drops
   upstream's 21 builtin specifications and adds Hive's two when the alias is
   tool capable. Position matters in both directions: after every upstream
   resolution step (server tools, MCP, direct tool servers, builtins) so
   nothing it produces is missed, and before `if tools_dict:` so the result is
   what lands in `metadata['tools']` and in `form_data['tools']`. Placed one
   line lower it would advertise Hive's tools without registering them, and the
   model's tool call would come back "Tool not found".

   It is spliced UNCONDITIONALLY at the handler's own indentation. The builtin
   injection directly above it is gated on `use_builtin_tools`, which requires a
   session id, non legacy function calling AND a model capability flag; hanging
   Hive's tools off that gate would make three unrelated conditions able to
   silently disable them, which is how #776 shipped inert. `assert_unconditional`
   below is what stops that.

2 and 3. The citation gate in the native tool loop. Upstream extracts citation
   sources only for tools it recognises BY NAME, and Hive's names are not
   upstream's: the specifications are `web_search` and `web_fetch`, upstream's
   builtins are `search_web` and `fetch_url`. Without these two edits a Hive
   search would return correct results to the model and produce no source chips
   at all, which is precisely the reported symptom of issue #1621. Edit 2 adds
   the two names to the gate; edit 3 normalises them onto upstream's names in
   the first statement of the extractor, so every parsing branch that already
   exists is reused rather than duplicated.

The transforms live in `patch()` so scripts/test_owui_web_tools.py can run the
real thing against the vendored copy of the pinned image's middleware.py. PR CI
never builds this image, so without that the patch would first be exercised at
deploy time.
"""

import ast
import os
import pathlib
import re
import sys

TARGET = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_MIDDLEWARE_PY",
        "/app/backend/open_webui/utils/middleware.py",
    )
)

MARKER = "# hive (#1718)"

SIGNATURE = "async def process_chat_payload(request, form_data, user, metadata, model):\n"

# Anchor 1: the branch that publishes the resolved tool set. Everything the
# splice changes has to be in place before this line runs.
ANCHOR = "        if tools_dict:\n"

# The two lines inside that branch which consume the tool set. Asserted so a
# future upstream change that stops reading `tools_dict` here turns the build
# red instead of shipping a splice that decorates a dead variable.
METADATA_WRITE = "            metadata['tools'] = tools_dict\n"
NATIVE_ATTACH = (
    "                form_data['tools'] = [\n"
    "                    {'type': 'function', 'function': tool.get('spec', {})} for tool in tools_dict.values()\n"
    "                ]\n"
)

CALL = "        tools_dict = await _hive_select_tools(request, tools_dict, model, metadata, user)\n"

INSERT = (
    f"""        {MARKER}: advertise Hive's two web tools to every tool capable
        # model, and drop upstream's builtin specifications. The model decides
        # per turn whether to call one; no toggle gates this. Upstream's own
        # native tool loop in process_chat_response executes whatever comes
        # back, because these entries are in the shape it already reads.
        #
        # The builtin drop is what makes native function calling affordable at
        # all on this deployment: 21 specifications is 12089 bytes and 3144
        # Groq prompt tokens per request, which OpenRouter answered 404 and the
        # Groq free tier answered 429. Two specifications is under 1200 bytes.
        from open_webui.utils.hive_web_tools import (
            override_instruction as _hive_override_instruction,
            select_tools as _hive_select_tools,
        )

"""
    + CALL
    + """        _hive_override = _hive_override_instruction(features, tools_dict)
        if _hive_override:
            # The globe toggle survives as an override rather than a gate: on,
            # the user is insisting on live results for this message; off,
            # the model decides alone. Neither state can remove the tools.
            form_data['messages'] = add_or_update_system_message(
                _hive_override,
                form_data.get('messages', []),
                append=True,
            )

"""
)

# Anchor 2: the citation gate's own list of recognised tool names.
CITATION_GATE = "                                'search_web',\n"
CITATION_GATE_PATCHED = (
    "                                'search_web',\n"
    "                                # hive (#1718): Hive's own web tools, so a\n"
    "                                # search performed through the gateway\n"
    "                                # produces the same source chips a native\n"
    "                                # Open WebUI search does (issue #1621).\n"
    "                                'web_search',\n"
    "                                'web_fetch',\n"
)

# Anchor 3: the first statement of the extractor itself. Its whole dispatch is
# by upstream's own tool names, so Hive's two are normalised onto them once, at
# the top, and every branch below is reused unchanged. Kept identical to
# hive_web_tools.CITATION_ALIASES, which scripts/test_owui_web_tools.py asserts.
CITATION_HEAD = "    _EXPECTS_LIST = {'search_web', 'query_knowledge_files'}\n"
CITATION_HEAD_PATCHED = (
    "    # hive (#1718): Hive's web tools carry the gateway's own names, and\n"
    "    # every branch below dispatches on upstream's builtin names. Normalise\n"
    "    # once here so a Hive search produces the same source chips a native\n"
    "    # Open WebUI search does, instead of none at all (issue #1621).\n"
    "    tool_name = {'web_search': 'search_web', 'web_fetch': 'fetch_url'}.get(tool_name, tool_name)\n"
    "    _EXPECTS_LIST = {'search_web', 'query_knowledge_files'}\n"
)


def handler_body(text: str) -> str:
    """The source of process_chat_payload's own body."""
    assert text.count(SIGNATURE) == 1, (
        "process_chat_payload is not defined exactly once with the expected "
        "signature -- upstream open-webui source shifted, patch needs updating"
    )
    start = text.index(SIGNATURE) + len(SIGNATURE)
    next_top_level = re.search(r"\n@|\n\S", text[start:])
    end = start + next_top_level.start() if next_top_level else len(text)
    return text[start:end]


def assert_unconditional(body: str) -> None:
    """Fail unless the Hive selection runs on every request to this handler.

    The guard #776 did not have. Its mechanism was real code that a deployment
    flag switched off, and every test it shipped with still passed. Any future
    edit that puts this call behind a flag, a role check or a capability branch
    trips this, because the statement would no longer sit at the handler's own
    indentation level.
    """
    for line in body.splitlines(keepends=True):
        if line.lstrip().startswith("tools_dict = await _hive_select_tools("):
            assert line == CALL, (
                "the hive web tool selection is indented deeper than the "
                "handler body, so something now gates it. It must run for "
                "every chat request: the whole point of issue #1718 is that no "
                "toggle, flag or user setting decides whether a model is told "
                "these tools exist."
            )
            return
    raise AssertionError("the hive web tool selection is not in process_chat_payload")


def patch(text: str) -> str:
    """Return middleware.py with all three edits applied."""
    assert MARKER not in text, f"{MARKER} is already present -- patch applied twice"

    body = handler_body(text)

    assert text.count(ANCHOR) == 1, (
        "the 'if tools_dict:' anchor is not present exactly once -- upstream "
        "open-webui source shifted, patch needs updating"
    )
    assert ANCHOR in body, (
        "the 'if tools_dict:' anchor is not inside process_chat_payload's own "
        "body -- upstream open-webui source shifted, patch needs updating"
    )
    assert METADATA_WRITE in body, (
        "process_chat_payload no longer publishes the resolved tools as "
        "metadata['tools'], which is what the native tool loop executes from. "
        "Splicing here would advertise tools that cannot then be called"
    )
    assert NATIVE_ATTACH in body, (
        "process_chat_payload no longer builds form_data['tools'] from "
        "tools_dict, so the specifications would never reach the model -- "
        "upstream open-webui source shifted, patch needs updating"
    )
    assert "    features = form_data.pop('features', None) or {}\n" in body, (
        "process_chat_payload no longer resolves `features`, which the toggle "
        "override reads -- patch needs updating"
    )
    assert "    add_or_update_system_message,\n" in text, (
        "middleware.py no longer imports add_or_update_system_message -- patch "
        "needs updating"
    )
    assert text.count(CITATION_GATE) == 1, (
        "the citation gate's 'search_web' entry is not present exactly once -- "
        "upstream open-webui source shifted, patch needs updating"
    )
    assert text.count(CITATION_HEAD) == 1, (
        "get_citation_source_from_tool_result no longer opens with its "
        "_EXPECTS_LIST assignment -- upstream open-webui source shifted, patch "
        "needs updating"
    )
    assert "def get_citation_source_from_tool_result(" in text, (
        "middleware.py no longer defines get_citation_source_from_tool_result "
        "-- patch needs updating"
    )
    # The two branches the alias mapping exists to reuse. Mapping onto a name
    # whose branch has been removed would silently produce no citations again.
    assert "        if tool_name == 'search_web':\n" in text, (
        "the search_web citation branch is gone, so mapping web_search onto it "
        "would produce no sources -- patch needs updating"
    )
    assert "        elif tool_name == 'fetch_url':\n" in text, (
        "the fetch_url citation branch is gone, so mapping web_fetch onto it "
        "would produce no sources -- patch needs updating"
    )

    patched = text.replace(ANCHOR, INSERT + ANCHOR, 1)
    patched = patched.replace(CITATION_GATE, CITATION_GATE_PATCHED, 1)
    patched = patched.replace(CITATION_HEAD, CITATION_HEAD_PATCHED, 1)

    patched_body = handler_body(patched)
    assert_unconditional(patched_body)
    assert patched_body.index(CALL) < patched_body.index(ANCHOR), (
        "the selection must run before the branch that publishes the tool set, "
        "or the request carries the tools upstream resolved rather than the "
        "ones this deployment decided on"
    )
    ast.parse(patched)  # never write a middleware.py that cannot be imported
    return patched


if __name__ == "__main__":
    TARGET.write_text(patch(TARGET.read_text()))
    sys.stdout.write("hive #1718: web tool advertisement and citation mapping spliced into middleware.py\n")
