#!/usr/bin/env python3
"""Self-check for web_search and web_fetch reaching a model, end to end
through the path that ships them (issue #1718).

The thing that makes this test worth having is what it refuses to settle for.
A check that a descriptor list serialises proves nothing: that was already true
on main, where `Descriptors()` had no non test caller at all. So every
assertion below is about the CHAIN, and the two halves that matter are both
executed rather than described:

  advertisement   GET /v1/tools on a real local server -> select_tools ->
                  the exact list comprehension the patched middleware builds
                  form_data['tools'] from, extracted from the patched source so
                  it cannot drift -> both specifications present, with their
                  arguments.

  execution       the exact statements upstream's native tool loop runs, again
                  read out of the patched source -> our callable -> a real POST
                  to a local stand in for the gateway, with the shim key, the
                  user's own token and the turn header on it -> a result string
                  -> upstream's real citation extractor, exec'd from the
                  patched source, returning sources with URLs in them.

No framework, no network beyond loopback, no third party import.
Run: python3 scripts/test_owui_web_tools.py
"""

import ast
import asyncio
import hashlib
import importlib.util
import json
import logging
import os
import re
import shutil
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
PATCHES = REPO / "deploy" / "docker" / "owui-patches"
PATCH = PATCHES / "apply_web_tools_patch.py"
MODULE = PATCHES / "hive_web_tools.py"
VENDORED_MIDDLEWARE = REPO / "vendor" / "open-webui" / "backend" / "open_webui" / "utils" / "middleware.py"
PINNED_DIGESTS = PATCHES / "pinned-openai-digest.json"
DESCRIPTOR_GO = REPO / "apps" / "edge-api" / "internal" / "webtools" / "descriptor.go"
TYPES_GO = REPO / "apps" / "edge-api" / "internal" / "webtools" / "types.go"
HANDLER_GO = REPO / "apps" / "edge-api" / "internal" / "webtools" / "handler.go"
UNWRAP_GO = REPO / "apps" / "edge-api" / "internal" / "auth" / "owui_unwrap.go"
COMPOSE = REPO / "deploy" / "docker" / "docker-compose.yml"
DOCKERFILE = REPO / "deploy" / "docker" / "Dockerfile.open-webui"


def load(path: Path, name: str):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


web_tools = load(MODULE, "hive_web_tools_under_test")
patch_module = load(PATCH, "apply_web_tools_patch_under_test")


async def _fake_user_token(request, user):
    """Stand in for the signed-in user's token.

    The real resolver imports open_webui.utils.middleware, which is not
    importable outside the container. What that costs is covered by
    test_the_credential_comes_from_open_webuis_own_resolver below, which pins
    the one line this stub replaces.
    """
    return "test-access-token"


web_tools._user_token = _fake_user_token


# ---------------------------------------------------------------- the splice


def patched_middleware() -> str:
    """The vendored middleware with the real patch script applied to it."""
    with tempfile.TemporaryDirectory() as tmp:
        copy = Path(tmp) / "middleware.py"
        shutil.copyfile(VENDORED_MIDDLEWARE, copy)
        return patch_module.patch(copy.read_text(encoding="utf-8"))


PATCHED = patched_middleware()


def test_the_vendored_middleware_is_the_one_the_image_runs() -> None:
    """PR CI never builds Dockerfile.open-webui, so this whole file is evidence
    about the running container only while the vendored copy and the pinned
    image agree. Same device as pinned-chat-digest.json."""
    pinned = json.loads(PINNED_DIGESTS.read_text(encoding="utf-8"))
    expected = pinned["files"]["/app/backend/open_webui/utils/middleware.py"]["sha256"]
    actual = hashlib.sha256(VENDORED_MIDDLEWARE.read_bytes()).hexdigest()
    assert actual == expected, (
        "vendor middleware.py no longer matches the pinned image digest, so the "
        "patch was verified against source the container does not run. "
        f"expected {expected}, got {actual}"
    )


def test_the_patch_is_applied_by_the_image_build() -> None:
    dockerfile = DOCKERFILE.read_text(encoding="utf-8")
    assert "apply_web_tools_patch.py" in dockerfile, "the patch is never run by the image build"
    assert "hive_web_tools.py /app/backend/open_webui/utils/hive_web_tools.py" in dockerfile, (
        "the module the splice imports is never copied into the image"
    )
    assert "-eq 3" in dockerfile and "# hive (#1718)" in dockerfile, (
        "the build does not assert the splice landed, so a moved anchor would "
        "ship an image with no web tools rather than failing the build"
    )
    assert PATCHED.count("# hive (#1718)") == 3, "the patch no longer makes all three edits"


def test_the_splice_is_unconditional() -> None:
    """No flag, role, capability or toggle may gate the selection call. #776
    shipped inert because a deployment flag switched its mechanism off."""
    body = patch_module.handler_body(PATCHED)
    patch_module.assert_unconditional(body)  # raises if it is indented deeper
    assert body.count(patch_module.CALL) == 1


def test_the_selection_runs_before_the_tools_are_published() -> None:
    body = patch_module.handler_body(PATCHED)
    assert body.index(patch_module.CALL) < body.index(patch_module.ANCHOR)
    assert body.index(patch_module.CALL) < body.index(patch_module.NATIVE_ATTACH), (
        "the selection must run before form_data['tools'] is built, or the "
        "model receives the tools upstream resolved rather than Hive's"
    )
    assert body.index(patch_module.CALL) < body.index(patch_module.METADATA_WRITE), (
        "the selection must run before metadata['tools'] is written, or the "
        "tool loop cannot execute what the model calls back"
    )


def test_upstream_still_executes_what_it_publishes() -> None:
    """The contract this whole design rests on: Open WebUI's own native tool
    loop reads metadata['tools'], calls the entry's callable, and appends the
    result to the turn. If any of these lines moves, the tools are advertised
    and then not callable, which is worse than not advertising them."""
    for line in (
        "                    tools = metadata.get('tools', {})\n",
        "                                        function=tool['callable'],\n",
        "                                    tool_result = await tool_function(**tool_function_params)\n",
        "                                'type': 'function_call_output',\n",
    ):
        assert line in PATCHED, f"upstream's tool loop no longer contains: {line.strip()}"


def test_the_credential_comes_from_open_webuis_own_resolver() -> None:
    """The one line _fake_user_token above stands in for. All three Hive shims
    (this one, hive_agent_proxy, hive_upstream_auth) must resolve the signed-in
    user through the same function, or they drift into different answers to
    "who is this call for" and the spend is attributed to the wrong account."""
    source = MODULE.read_text(encoding="utf-8")
    assert "from open_webui.utils.middleware import get_system_oauth_token" in source
    assert "access_token" in source
    proxy = (PATCHES / "hive_agent_proxy.py").read_text(encoding="utf-8")
    assert "from open_webui.utils.middleware import get_system_oauth_token" in proxy, (
        "the agent proxy no longer uses this resolver, so the two shims have drifted"
    )


def test_the_names_agree_with_the_gateway() -> None:
    types_go = TYPES_GO.read_text(encoding="utf-8")
    assert f'ToolWebSearch = "{web_tools.WEB_SEARCH}"' in types_go
    assert f'ToolWebFetch  = "{web_tools.WEB_FETCH}"' in types_go
    handler_go = HANDLER_GO.read_text(encoding="utf-8")
    assert 'mux.HandleFunc("/v1/tools", h.handleList)' in handler_go, (
        "the gateway no longer serves the descriptor list this module reads"
    )
    assert f'TurnHeader = "{web_tools.TURN_HEADER}"' in handler_go
    unwrap_go = UNWRAP_GO.read_text(encoding="utf-8")
    assert 'strings.HasPrefix(path, "/v1/tools/")' in unwrap_go, (
        "a shim-key web tool call with no per-user token would be served under "
        "the shim account's principal and billed to it"
    )
    assert web_tools.CITATION_ALIASES == {"web_search": "search_web", "web_fetch": "fetch_url"}
    assert "{'web_search': 'search_web', 'web_fetch': 'fetch_url'}" in PATCHED, (
        "the citation alias map in the patch has drifted from CITATION_ALIASES"
    )


def test_the_specifications_are_not_copied_into_the_shim() -> None:
    """The endpoint exists so there is exactly one source. A hardcoded spec
    here would keep looking right forever after the handler changed."""
    source = MODULE.read_text(encoding="utf-8")
    for phrase in ("Search the live web", "Fetch one http(s) URL", '"parameters"', "max_results\": {"):
        assert phrase not in source, f"the shim carries its own copy of the tool spec: {phrase!r}"
    go = DESCRIPTOR_GO.read_text(encoding="utf-8")
    assert "Search the live web" in go, "the descriptions no longer live in Go"


# --------------------------------------------------------- a stand-in gateway


SEARCH_ENVELOPE = {
    "status": "ok",
    "query": "who won the 2026 world cup",
    "results": [
        {
            "title": "Final result",
            "url": "https://example.org/final",
            "snippet": "The tournament ended on 19 July 2026.",
            "rank": 1,
        },
        {
            "title": "Match report",
            "url": "https://example.net/report",
            "snippet": "A full report of the final.",
            "rank": 2,
        },
    ],
    "dropped": 0,
}

FETCH_ENVELOPE = {
    "status": "ok",
    "url": "https://example.org/final",
    "final_url": "https://example.org/final",
    "title": "Final result",
    "parts": [
        {
            "text": "[BEGIN UNTRUSTED WEB CONTENT deadbeefdeadbeef]\nThe match ended 2-1.\n[END UNTRUSTED WEB CONTENT deadbeefdeadbeef]",
            "start": 0,
            "end": 20,
        }
    ],
    "truncated": False,
    "total_chars": 20,
    "retrieved_chars": 20,
    "dropped": 0,
}

DESCRIPTORS = {
    "object": "list",
    "data": [
        {
            "type": "function",
            "function": {
                "name": "web_search",
                "description": "Search the live web.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "The search query."},
                        "max_results": {"type": "integer", "description": "How many results."},
                    },
                    "required": ["query"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "web_fetch",
                "description": "Fetch one http(s) URL.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "url": {"type": "string", "description": "The absolute URL."},
                        "focus": {"type": "string", "description": "What to look for."},
                    },
                    "required": ["url"],
                },
            },
        },
    ],
}


class Gateway(ThreadingHTTPServer):
    daemon_threads = True
    seen: list = []
    refuse: dict = {}


class Route(BaseHTTPRequestHandler):
    def log_message(self, *args):  # keep the test output clean
        pass

    def _send(self, status, payload):
        raw = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path == "/v1/tools":
            self.server.seen.append(("GET", self.path, dict(self.headers), None))
            self._send(200, DESCRIPTORS)
            return
        self._send(404, {"status": "error", "code": "not_found", "message": "no"})

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(length) or "null")
        self.server.seen.append(("POST", self.path, dict(self.headers), body))
        if self.path in self.server.refuse:
            status, payload = self.server.refuse[self.path]
            self._send(status, payload)
            return
        if self.path == "/v1/tools/web_search":
            self._send(200, SEARCH_ENVELOPE)
            return
        if self.path == "/v1/tools/web_fetch":
            self._send(200, FETCH_ENVELOPE)
            return
        self._send(404, {"status": "error", "code": "not_found", "message": "no"})


class FakeUser:
    id = "user-1"


def run_gateway(fn, refuse=None):
    """Run fn(base_url, server) against a live loopback stand-in for edge-api."""
    server = Gateway(("127.0.0.1", 0), Route)
    server.seen = []
    server.refuse = refuse or {}
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        return fn(f"http://127.0.0.1:{server.server_port}/v1", server)
    finally:
        server.shutdown()
        server.server_close()


def environ_for(base: str) -> dict:
    return {"OPENAI_API_BASE_URL": base, "OPENAI_API_KEY": "hk_shim_test_key"}


def select(base: str, model: dict, tools_dict=None, environ=None, metadata=None):
    web_tools.reset_descriptor_cache()
    environ = environ or environ_for(base)
    # The real module reads os.environ inside the tool callables, so the
    # process environment has to agree with what select_tools was handed.
    os.environ.update({k: str(v) for k, v in environ.items()})
    return asyncio.run(
        web_tools.select_tools(
            request=object(),
            tools_dict=tools_dict or {},
            model=model,
            metadata=metadata if metadata is not None else {"message_id": "assistant-turn-1"},
            user=FakeUser(),
            environ=environ,
        )
    )


TOOL_CAPABLE = {"id": "hive-default", "hive_capabilities": {"tools": True}}
NOT_TOOL_CAPABLE = {"id": "hive-basic", "hive_capabilities": {"tools": False}}
NO_CAPABILITY_BLOCK = {"id": "some-preset"}


# ------------------------------------------------------------ advertisement


def native_tools_payload(tools_dict: dict) -> list:
    """Build form_data['tools'] with the EXACT comprehension the patched
    middleware uses, read out of the patched source. A reimplementation here
    could agree with a broken original."""
    match = re.search(
        r"form_data\['tools'\] = (\[\s*\{'type': 'function', 'function': tool\.get\('spec', \{\}\)\} "
        r"for tool in tools_dict\.values\(\)\s*\])",
        PATCHED,
    )
    assert match, "upstream no longer builds form_data['tools'] from tools_dict"
    return eval(match.group(1), {"tools_dict": tools_dict})  # noqa: S307 - source is the pinned image


def test_a_tool_capable_model_receives_both_specifications() -> None:
    def check(base, server):
        tools = select(base, TOOL_CAPABLE)
        payload = native_tools_payload(tools)
        by_name = {entry["function"]["name"]: entry for entry in payload}
        assert set(by_name) == {"web_search", "web_fetch"}, by_name
        for entry in payload:
            assert entry["type"] == "function"
            assert entry["function"]["description"], "a specification reached the model with no description"
            properties = entry["function"]["parameters"]["properties"]
            assert properties, "a specification reached the model with no arguments"
        assert set(by_name["web_search"]["function"]["parameters"]["properties"]) == {"query", "max_results"}
        assert set(by_name["web_fetch"]["function"]["parameters"]["properties"]) == {"url", "focus"}
        # And they came from the gateway, not from this repository's Python.
        assert ("GET", "/v1/tools", None, None) not in server.seen
        assert any(method == "GET" and path == "/v1/tools" for method, path, _, _ in server.seen), (
            "the specifications were not read from the gateway"
        )

    run_gateway(check)


def test_no_toggle_is_consulted() -> None:
    """The whole point of #1718. Nothing about features, user settings or the
    globe toggle reaches select_tools, so nothing about them can suppress the
    advertisement."""
    signature = str(web_tools.select_tools.__code__.co_varnames[: web_tools.select_tools.__code__.co_argcount])
    assert "features" not in signature, signature
    source = MODULE.read_text(encoding="utf-8")
    body = source[source.index("async def select_tools") :]
    for forbidden in ("features", "web_search_toggle", "user_settings"):
        assert forbidden not in body.split("def override_instruction")[0], (
            f"select_tools consults {forbidden}, so a toggle can still suppress the tools"
        )


def test_a_model_that_cannot_call_tools_is_offered_none() -> None:
    for model in (NOT_TOOL_CAPABLE, NO_CAPABILITY_BLOCK, {}, None):
        def check(base, server, model=model):
            tools = select(base, model)
            assert tools == {}, f"{model} was offered {sorted(tools)}"

        run_gateway(check)


def test_the_capability_survives_open_webui_model_merging() -> None:
    """Open WebUI merges the gateway's entry with `**model`, so the block is on
    the outer dict; the nested `openai` copy is the same entry untouched. Both
    are read, so an upstream change to either one alone cannot silently
    disable every tool."""
    assert web_tools.tool_capable({"hive_capabilities": {"tools": True}})
    assert web_tools.tool_capable({"openai": {"hive_capabilities": {"tools": True}}})
    assert not web_tools.tool_capable({"hive_capabilities": {"tools": "yes"}}), (
        "a non-boolean must not read as capable"
    )
    assert not web_tools.tool_capable({"hive_capabilities": {}})


def test_upstream_builtin_specifications_are_dropped() -> None:
    """The payload budget that pinned this deployment to the legacy path. 21
    builtin specifications is 12089 bytes; two is under 1200."""
    builtins = {
        "get_current_timestamp": {"spec": {"name": "get_current_timestamp"}, "type": "builtin"},
        "search_web": {"spec": {"name": "search_web"}, "type": "builtin"},
        "execute_code": {"spec": {"name": "execute_code"}, "type": "builtin"},
    }

    def check(base, server):
        tools = select(base, TOOL_CAPABLE, tools_dict=dict(builtins))
        assert set(tools) == {"web_search", "web_fetch"}, sorted(tools)

    run_gateway(check)


def test_a_turn_with_folder_knowledge_keeps_the_knowledge_tools() -> None:
    """Upstream stops injecting a folder's files into the request the moment
    function calling is native and hands the work to these tools. Dropping them
    on such a turn would silently strand the documents while the interface
    still offered them."""
    builtins = {
        "query_knowledge_files": {"spec": {"name": "query_knowledge_files"}, "type": "builtin"},
        "view_file": {"spec": {"name": "view_file"}, "type": "builtin"},
        "get_current_timestamp": {"spec": {"name": "get_current_timestamp"}, "type": "builtin"},
        "search_chats": {"spec": {"name": "search_chats"}, "type": "builtin"},
    }

    def check(base, server):
        metadata = {"message_id": "turn-1", "folder_knowledge": [{"type": "collection", "id": "kb-1"}]}
        tools = select(base, TOOL_CAPABLE, tools_dict=dict(builtins), metadata=metadata)
        assert "query_knowledge_files" in tools and "view_file" in tools, sorted(tools)
        # And nothing else came back with them.
        assert "get_current_timestamp" not in tools and "search_chats" not in tools, sorted(tools)
        assert {"web_search", "web_fetch"} <= set(tools)

    run_gateway(check)


def test_a_model_with_attached_knowledge_keeps_them_too() -> None:
    builtins = {"query_knowledge_files": {"spec": {"name": "query_knowledge_files"}, "type": "builtin"}}
    model = dict(TOOL_CAPABLE) | {"info": {"meta": {"knowledge": [{"type": "collection", "id": "kb"}]}}}

    def check(base, server):
        tools = select(base, model, tools_dict=dict(builtins))
        assert "query_knowledge_files" in tools, sorted(tools)

    run_gateway(check)


def test_an_ordinary_turn_carries_no_knowledge_tools() -> None:
    """The payload budget. Keeping them always would put a knowledge tool on
    every chat request from a user who has no documents at all."""
    builtins = {"query_knowledge_files": {"spec": {"name": "query_knowledge_files"}, "type": "builtin"}}

    def check(base, server):
        tools = select(base, TOOL_CAPABLE, tools_dict=dict(builtins))
        assert set(tools) == {"web_search", "web_fetch"}, sorted(tools)

    run_gateway(check)


def test_the_knowledge_tool_names_still_exist_upstream() -> None:
    """A rename upstream must fail a pull request, not quietly reopen the gap."""
    tools_py = (REPO / "vendor" / "open-webui" / "backend" / "open_webui" / "utils" / "tools.py").read_text(
        encoding="utf-8"
    )
    imports = tools_py[tools_py.index("from open_webui.tools.builtin import (") :]
    imports = imports[: imports.index(")")]
    declared = {line.strip().rstrip(",") for line in imports.splitlines()[1:]}
    missing = web_tools.KNOWLEDGE_TOOL_NAMES - declared
    assert not missing, f"these knowledge tools no longer exist upstream: {sorted(missing)}"


def test_the_two_unconditional_document_paths_are_untouched() -> None:
    """A file attached to the message, and a Hive project's files (PR #1707,
    appended to the same request `files` list), both go through
    chat_completion_files_handler. Upstream calls it on either path, so the
    native flip cannot strand them. Pinned, because that is the claim the
    knowledge handling above rests on."""
    body = patch_module.handler_body(PATCHED)
    handler_call = (
        "            form_data, flags = await chat_completion_files_handler("
        "request, form_data, extra_params, user)"
    )
    assert handler_call in body, "upstream no longer runs the request-files handler here"
    guard = body[: body.index(handler_call)].splitlines()[-6:]
    assert not any("legacy" in line for line in guard), (
        "the request-files handler is now gated on legacy function calling, so "
        f"the native flip would strand attached files: {guard}"
    )


def test_a_named_builtin_can_be_kept() -> None:
    builtins = {"query_knowledge_files": {"spec": {"name": "query_knowledge_files"}, "type": "builtin"}}

    def check(base, server):
        environ = environ_for(base) | {"HIVE_OWUI_BUILTIN_TOOLS": "query_knowledge_files"}
        tools = select(base, TOOL_CAPABLE, tools_dict=dict(builtins), environ=environ)
        assert "query_knowledge_files" in tools

    run_gateway(check)
    os.environ.pop("HIVE_OWUI_BUILTIN_TOOLS", None)


def test_a_user_attached_tool_is_never_replaced() -> None:
    mine = {"web_search": {"spec": {"name": "web_search"}, "type": "external", "callable": None}}

    def check(base, server):
        tools = select(base, TOOL_CAPABLE, tools_dict=dict(mine))
        assert tools["web_search"]["type"] == "external", "a user's own tool was displaced"
        assert "web_fetch" in tools

    run_gateway(check)


def test_an_unreachable_gateway_advertises_nothing() -> None:
    """Never a stale hardcoded fallback. Advertising a specification the
    gateway cannot serve produces a model that claims it searched."""
    web_tools.reset_descriptor_cache()
    environ = {"OPENAI_API_BASE_URL": "http://127.0.0.1:1/v1", "OPENAI_API_KEY": "hk_x"}
    os.environ.update(environ)
    tools = asyncio.run(
        web_tools.select_tools(object(), {}, TOOL_CAPABLE, {"message_id": "t"}, FakeUser(), environ)
    )
    assert tools == {}


def test_the_kill_switch_is_a_deployment_setting_not_a_user_one() -> None:
    def check(base, server):
        environ = environ_for(base) | {"HIVE_WEB_TOOLS_ENABLED": "false"}
        assert select(base, TOOL_CAPABLE, environ=environ) == {}

    run_gateway(check)
    os.environ.pop("HIVE_WEB_TOOLS_ENABLED", None)


# ---------------------------------------------------------------- execution


def loop_execute(tools: dict, name: str, arguments: dict):
    """Run the statements upstream's native tool loop runs, in its own order:
    resolve the entry from metadata['tools'], drop arguments the spec does not
    declare, then await the callable."""
    metadata_tools = tools  # what `metadata['tools'] = tools_dict` publishes
    assert name in metadata_tools, f"the model called {name} and the loop would answer Tool not found"
    tool = metadata_tools[name]
    allowed = tool.get("spec", {}).get("parameters", {}).get("properties", {}).keys()
    params = {k: v for k, v in arguments.items() if k in allowed}
    return asyncio.run(tool["callable"](**params))


def test_a_search_the_model_calls_is_executed_and_comes_back_into_the_turn() -> None:
    def check(base, server):
        tools = select(base, TOOL_CAPABLE)
        result = loop_execute(tools, "web_search", {"query": "who won the 2026 world cup", "max_results": 3})

        posts = [entry for entry in server.seen if entry[0] == "POST"]
        assert len(posts) == 1, posts
        _, path, headers, body = posts[0]
        assert path == "/v1/tools/web_search", path
        assert body == {"query": "who won the 2026 world cup", "max_results": 3}, body
        assert headers["Authorization"] == "Bearer hk_shim_test_key", "the shim key did not travel"
        assert headers[web_tools.UPSTREAM_AUTH_HEADER] == "Bearer test-access-token", (
            "the signed-in user's own token did not travel, so the spend would be billed to the shim account"
        )
        assert headers[web_tools.TURN_HEADER] == "assistant-turn-1", "no turn, so the per-turn budget fails open"

        # What returns into the turn. Upstream appends str(tool_result) as the
        # function_call_output, so this string is what the model reads next.
        parsed = json.loads(result)
        assert [hit["link"] for hit in parsed] == ["https://example.org/final", "https://example.net/report"]
        assert parsed[0]["title"] == "Final result"
        assert parsed[0]["snippet"]

    run_gateway(check)


def test_a_fetch_the_model_calls_returns_the_page_with_its_fence_intact() -> None:
    def check(base, server):
        tools = select(base, TOOL_CAPABLE)
        result = loop_execute(tools, "web_fetch", {"url": "https://example.org/final", "focus": "score"})

        posts = [entry for entry in server.seen if entry[0] == "POST"]
        assert posts[0][1] == "/v1/tools/web_fetch"
        assert posts[0][3] == {"url": "https://example.org/final", "focus": "score"}
        assert "BEGIN UNTRUSTED WEB CONTENT deadbeefdeadbeef" in result, (
            "the gateway's per-call content fence was stripped, so an injected "
            "page can present itself as being outside it"
        )
        assert "END UNTRUSTED WEB CONTENT deadbeefdeadbeef" in result
        assert "https://example.org/final" in result

    run_gateway(check)


def test_an_argument_the_specification_does_not_declare_is_dropped() -> None:
    def check(base, server):
        tools = select(base, TOOL_CAPABLE)
        loop_execute(tools, "web_search", {"query": "x", "callback_url": "https://attacker.example/steal"})
        body = [entry for entry in server.seen if entry[0] == "POST"][0][3]
        assert "callback_url" not in body, body

    run_gateway(check)


def test_a_refusal_reaches_the_model_as_its_own_reason() -> None:
    """D-034. A call that cannot be priced is refused rather than served free,
    and the model is told which refusal it was rather than a generic failure."""
    refusals = {
        "/v1/tools/web_search": (
            402,
            {
                "status": "error",
                "code": "insufficient_credit",
                "message": "Your available credit does not cover this web tool call. Add credits and try again.",
                "dropped": 0,
            },
        )
    }

    def check(base, server):
        tools = select(base, TOOL_CAPABLE)
        result = loop_execute(tools, "web_search", {"query": "x"})
        parsed = json.loads(result)
        assert parsed["code"] == "insufficient_credit", parsed
        assert "Add credits" in parsed["error"], parsed
        assert "127.0.0.1" not in result and "/v1/tools" not in result, (
            "an internal address leaked into what the model was told"
        )

    run_gateway(check, refuse=refusals)


def test_a_call_without_a_resolvable_user_token_is_never_made() -> None:
    """Fail closed. Without the user's token the gateway refuses the call
    anyway; making it regardless would, in any future where it did not, bill
    one account for every customer's searches."""
    def check(base, server):
        tools = select(base, TOOL_CAPABLE)
        original = web_tools._user_token
        web_tools._user_token = _no_token
        try:
            result = loop_execute(tools, "web_search", {"query": "x"})
        finally:
            web_tools._user_token = original
        assert [entry for entry in server.seen if entry[0] == "POST"] == [], "a call was made with no credential"
        assert "credential" in json.loads(result)["error"]

    run_gateway(check)


def test_a_turn_with_no_identifier_is_refused_before_the_call() -> None:
    def check(base, server):
        tools = select(base, TOOL_CAPABLE, metadata={})
        result = loop_execute(tools, "web_search", {"query": "x"})
        assert [entry for entry in server.seen if entry[0] == "POST"] == [], (
            "a call with no turn identifier was sent, and the per-turn budget cannot bound it"
        )
        assert "turn identifier" in json.loads(result)["error"]

    run_gateway(check)


# ----------------------------------------------------------------- citations


def citation_extractor():
    """Upstream's own get_citation_source_from_tool_result, taken from the
    PATCHED source and executed. Not a reimplementation: a reimplementation
    could agree with a broken original."""
    tree = ast.parse(PATCHED)
    for node in tree.body:
        if isinstance(node, ast.FunctionDef) and node.name == "get_citation_source_from_tool_result":
            namespace = {"json": json, "log": logging.getLogger("test")}
            exec(compile(ast.Module([node], []), "<patched middleware>", "exec"), namespace)  # noqa: S102
            return namespace["get_citation_source_from_tool_result"]
    raise AssertionError("get_citation_source_from_tool_result is gone from middleware.py")


def test_a_hive_search_produces_the_same_sources_a_native_one_would() -> None:
    """Issue #1621's symptom is answers with no sources. Upstream extracts
    citations only for tools it knows by name, and Hive's names are not
    upstream's, so without the alias normalisation a correct search shows no
    sources at all."""
    extract = citation_extractor()

    def check(base, server):
        tools = select(base, TOOL_CAPABLE)
        result = loop_execute(tools, "web_search", {"query": "who won"})
        sources = extract(tool_name="web_search", tool_params={"query": "who won"}, tool_result=result)
        assert sources, "a Hive web search produced no citation sources"
        entries = sources[0].get("metadata") or []
        assert entries and all("url" in entry for entry in entries), (
            "the citation sources carry no url, so the answer shows no source "
            f"chips: {sources}"
        )
        urls = [entry["url"] for entry in entries]
        assert urls == ["https://example.org/final", "https://example.net/report"], urls
        assert sources[0]["document"], "the citation carries no document text"

    run_gateway(check)


def test_a_hive_fetch_produces_a_source_for_the_page() -> None:
    extract = citation_extractor()

    def check(base, server):
        tools = select(base, TOOL_CAPABLE)
        result = loop_execute(tools, "web_fetch", {"url": "https://example.org/final"})
        sources = extract(
            tool_name="web_fetch",
            tool_params={"url": "https://example.org/final"},
            tool_result=result,
        )
        assert sources, "a Hive web fetch produced no citation source"
        assert sources[0]["metadata"][0]["url"] == "https://example.org/final"

    run_gateway(check)


def test_a_refusal_never_becomes_a_citation() -> None:
    extract = citation_extractor()
    assert extract(tool_name="web_search", tool_params={}, tool_result=json.dumps({"error": "no"})) == []


def test_the_citation_gate_admits_both_hive_names() -> None:
    gate = PATCHED[PATCHED.index("citation_sources = get_citation_source_from_tool_result") - 2000 :][:2000]
    assert "'web_search'," in gate and "'web_fetch'," in gate, (
        "the citation gate does not admit Hive's tool names, so no source chip is built"
    )


# ------------------------------------------------------------------- override


def test_the_globe_toggle_is_an_override_and_never_a_gate() -> None:
    tools = {"web_search": {}, "web_fetch": {}}
    assert web_tools.override_instruction({}, tools) == ""
    assert web_tools.override_instruction({"web_search": False}, tools) == ""
    forced = web_tools.override_instruction({"web_search": True}, tools)
    assert "web_search" in forced and "live web results" in forced
    # And with no tools attached it adds nothing, so a model that cannot search
    # is never told to search.
    assert web_tools.override_instruction({"web_search": True}, {}) == ""


def test_the_override_is_appended_and_cannot_delete_the_deployment_prompt() -> None:
    body = patch_module.handler_body(PATCHED)
    assert "append=True," in body[body.index(patch_module.CALL) :][:1200], (
        "the toggle override is not appended, so it could displace the "
        "deployment's own system prompt (issue #1596)"
    )


# ------------------------------------------------------------------- compose


def test_native_function_calling_is_the_default() -> None:
    """Under legacy, utils/middleware.py gates the entire form_data['tools']
    attachment away, so no specification reaches a model at any price."""
    compose = COMPOSE.read_text(encoding="utf-8")
    assert "HIVE_DEFAULT_FUNCTION_CALLING: ${OWUI_DEFAULT_FUNCTION_CALLING:-native}" in compose
    assert "HIVE_WEB_TOOLS_ENABLED: ${OWUI_WEB_TOOLS_ENABLED:-true}" in compose
    assert "HIVE_OWUI_BUILTIN_TOOLS: ${OWUI_BUILTIN_TOOLS:-}" in compose
    assert (
        "params.function_calling != 'legacy'" in PATCHED or "function_calling') != 'legacy'" in PATCHED
    ), "upstream no longer gates native tool attachment on this value"


async def _no_token(request, user):
    return ""


def main() -> None:
    # The module logs loudly on every degradation, which several tests provoke
    # deliberately. Silence its logger only, so a real traceback from the test
    # itself is still the only thing on stderr.
    logging.getLogger("hive_web_tools_under_test").disabled = True
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            try:
                fn()
            except Exception as error:  # name the check that failed
                raise AssertionError(f"{name}: {error!r}") from error
    print("ok: owui web tools advertised, executed and cited (issue #1718)")


if __name__ == "__main__":
    sys.exit(main())
