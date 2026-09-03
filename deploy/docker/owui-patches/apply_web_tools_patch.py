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

Four edits, all inside utils/middleware.py.

1. `process_chat_payload`, immediately above `if tools_dict:`. Replaces the
   resolved tool set with `hive_web_tools.select_tools(...)`, which drops
   upstream's 21 builtin specifications and adds Hive's two when the alias is
   tool capable. Position matters in both directions: after every upstream
   resolution step (server tools, MCP, direct tool servers, builtins) so
   nothing it produces is missed, and before `if tools_dict:` so the result is
   what lands in `metadata['tools']` and in `form_data['tools']`. Placed one
   line lower it would advertise Hive's tools without registering them, and the
   model's tool call would come back "Tool not found".

   Its ONLY gate is upstream's own `if payload_tools is None:`, the branch that
   skips all server side tool resolution when the caller supplied an explicit
   `tools` key. That gate is deliberate: a request that brought its own tools
   has opted out, and `tools_dict` does not even exist outside it. Nothing
   else may gate it. The builtin injection directly above it is gated on
   `use_builtin_tools`, which requires a session id, non legacy function
   calling AND a model capability flag; hanging Hive's tools off that gate
   would make three unrelated conditions able to silently disable them, which
   is how #776 shipped inert. `assert_selection_gate` below parses the patched
   module and fails unless the chain of statements enclosing the call is
   exactly that one `if`, so a second condition added at the same indentation
   fails the image build instead of quietly narrowing the feature. An early
   `return` above the call, inside that same branch, fails it too: the chain
   by itself does not prove the call is reached, and
   scripts/test_owui_task_upstream_auth.py pins that same shape one file over
   for the same reason.

2 and 3. The citation gate in the native tool loop. Upstream extracts citation
   sources only for tools it recognises BY NAME, and Hive's names are not
   upstream's: the specifications are `web_search` and `web_fetch`, upstream's
   builtins are `search_web` and `fetch_url`. Without these two edits a Hive
   search would return correct results to the model and produce no source chips
   at all, which is precisely the reported symptom of issue #1621. Edit 2 adds
   the two names to the gate; edit 3 normalises them onto upstream's names in
   the first statement of the extractor, so every parsing branch that already
   exists is reused rather than duplicated.

4. Earlier in the same handler, above the first statement that reads
   `params.function_calling`. A turn Hive's web tools cannot be attached to at
   all (the kill switch, an alias whose routes report no tool support, or a
   gateway that would not serve the specifications) is put back on Open WebUI's
   legacy path. Without it, such a turn sits on the native path carrying no
   tools, and every call site of `chat_web_search_handler` is gated on legacy,
   so the globe toggle becomes a visible control wired to nothing and the kill
   switch removes web search from the product instead of restoring what
   preceded this feature. Position matters as much as it does for edit 1: the
   downgrade has to happen before the folder knowledge branch reads the value,
   or one turn would answer the question two different ways and a folder's
   files would be stranded between the two.

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

# Anchor 4: the first thing process_chat_payload does after resolving which
# model this turn actually runs on (arena wrappers pick a sub-model above it).
# Everything below it that branches on function calling has to see one answer,
# and the folder knowledge branch a few lines down is the first such reader.
LEGACY_ANCHOR = '    # Folder "Project" handling\n'

DOWNGRADE_CALL = "    if form_data.get('tools') is None and await _hive_prefer_legacy(model):\n"

# The first read of the value the downgrade writes. Asserted to come after it,
# because a downgrade applied later would leave one turn answering the question
# two ways.
FIRST_FUNCTION_CALLING_READ = ".get('function_calling')"

LEGACY_INSERT = (
    f"""    {MARKER}: a turn Hive's web tools cannot be put on runs on Open
    # WebUI's own legacy path rather than on a native one with nothing on it.
    # Three causes, one shape: the HIVE_WEB_TOOLS_ENABLED kill switch, an alias
    # whose routes report no tool support, and a gateway that would not serve
    # the specifications. Native buys such a turn nothing and costs it the
    # globe toggle, since every call site of chat_web_search_handler below is
    # gated on legacy; downgrading restores exactly what this deployment did
    # before the web tools existed. `form_data.get('tools')` is the same
    # snapshot `payload_tools` takes a few dozen lines down, read early because
    # this has to run above the first branch on function calling.
    from open_webui.utils.hive_web_tools import prefer_legacy as _hive_prefer_legacy

"""
    + DOWNGRADE_CALL
    + """        metadata.setdefault('params', {})['function_calling'] = 'legacy'

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


# The one gate the selection is allowed to sit behind, as upstream writes it.
ONLY_PERMITTED_GATE = "payload_tools is None"


# Statements that open a scope of their own. A `return` inside one of these
# belongs to that function, not to the path through the handler: upstream's own
# `tool_function` closure sits above the splice and returns twice.
NESTED_SCOPES = (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda, ast.ClassDef)


def early_returns(body: list, call) -> list:
    """Every `return` that would run before `call` does, in `call`'s own branch.

    An exit above the splice leaves the enclosing chain untouched and the call
    dead, so the chain by itself does not prove the call is reached. Nested
    scopes are skipped, and so is everything at or below `call`.
    """
    found = []
    for node in body:
        if node is call:
            break
        if isinstance(node, NESTED_SCOPES):
            continue
        if isinstance(node, ast.Return):
            found.append(node)
        for field in ("body", "orelse", "finalbody", "handlers"):
            inner = getattr(node, field, None)
            if isinstance(inner, list) and inner:
                found += early_returns(inner, call)
    return found


def selection_gates(text: str) -> list:
    """What stands between process_chat_payload being entered and the Hive
    selection call running, outermost first.

    Two kinds of entry, in one list, because either alone decides whether a
    model is told the tools exist. The statements ENCLOSING the call, rendered
    as source (an `if` becomes its test, anything else becomes its node type,
    so an unexpected `try` or `for` is as visible as an unexpected condition),
    and any `return` that would run BEFORE it inside the same branch, rendered
    as `Return`.
    """
    tree = ast.parse(text)
    handler = next(
        (
            node
            for node in ast.walk(tree)
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == "process_chat_payload"
        ),
        None,
    )
    assert handler is not None, "process_chat_payload is gone from middleware.py"

    def walk(body, chain):
        for node in body:
            # The assignment itself, never a compound statement that merely
            # contains it: unparsing an `if` yields its whole block, which
            # would report the call as sitting outside its own gate.
            if isinstance(node, ast.Assign) and "_hive_select_tools(" in ast.unparse(node.value):
                return chain + early_returns(body, node)
            for field in ("body", "orelse", "finalbody", "handlers"):
                inner = getattr(node, field, None)
                if isinstance(inner, list) and inner:
                    found = walk(inner, chain + [node])
                    if found is not None:
                        return found
        return None

    chain = walk(handler.body, [])
    assert chain is not None, "the hive web tool selection is not in process_chat_payload"
    return [ast.unparse(node.test) if isinstance(node, ast.If) else type(node).__name__ for node in chain]


def assert_selection_gate(text: str) -> None:
    """Fail unless the selection runs unconditionally inside upstream's own
    `payload_tools` branch.

    The guard #776 did not have. Its mechanism was real code that a deployment
    flag switched off, and every test it shipped with still passed.

    What is pinned is the exact chain of statements the call sits inside, taken
    from the parsed module rather than from its indentation. `payload_tools is
    None` is upstream's own branch for "the caller did not supply tools", and
    it is where `tools_dict` exists at all; the earlier version of this check
    compared indentation, which cannot tell that branch apart from a second
    condition added beside it. Anything else in the chain, at any depth, fails
    the image build: no toggle, flag, role or capability may decide whether a
    model is told these tools exist.

    An early `return` above the call, inside that same branch, fails it too. It
    leaves the chain reading exactly right while the call never runs, which is
    the one shape parsing the chain alone does not catch.
    """
    gates = selection_gates(text)
    assert gates == [ONLY_PERMITTED_GATE], (
        "the hive web tool selection does not run unconditionally inside "
        f"upstream's own `{ONLY_PERMITTED_GATE}` branch: {gates}. Anything "
        "beyond that one entry is either a second gate around the call or an "
        "early exit above it, and both let some condition decide whether a "
        "model is told these tools exist. The whole point of issue #1718 is "
        "that none may."
    )


def patch(text: str) -> str:
    """Return middleware.py with all four edits applied."""
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
    assert text.count(LEGACY_ANCHOR) == 1, (
        "the folder handling anchor is not present exactly once -- upstream "
        "open-webui source shifted, patch needs updating"
    )
    assert LEGACY_ANCHOR in body, (
        "the folder handling anchor is not inside process_chat_payload's own "
        "body -- upstream open-webui source shifted, patch needs updating"
    )
    assert "    payload_tools = form_data.get('tools', None)" in body, (
        "process_chat_payload no longer snapshots the caller's own tools as "
        "payload_tools, which is the condition the downgrade reads early -- "
        "patch needs updating"
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
    patched = patched.replace(LEGACY_ANCHOR, LEGACY_INSERT + LEGACY_ANCHOR, 1)
    patched = patched.replace(CITATION_GATE, CITATION_GATE_PATCHED, 1)
    patched = patched.replace(CITATION_HEAD, CITATION_HEAD_PATCHED, 1)

    patched_body = handler_body(patched)
    assert_selection_gate(patched)
    assert patched_body.index(CALL) < patched_body.index(ANCHOR), (
        "the selection must run before the branch that publishes the tool set, "
        "or the request carries the tools upstream resolved rather than the "
        "ones this deployment decided on"
    )
    assert patched_body.index(DOWNGRADE_CALL) < patched_body.index(FIRST_FUNCTION_CALLING_READ), (
        "the legacy downgrade runs after something has already branched on "
        "function calling, so one turn would answer that question two "
        "different ways"
    )
    ast.parse(patched)  # never write a middleware.py that cannot be imported
    return patched


if __name__ == "__main__":
    TARGET.write_text(patch(TARGET.read_text()))
    sys.stdout.write("hive #1718: web tool advertisement and citation mapping spliced into middleware.py\n")
