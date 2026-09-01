#!/usr/bin/env python3
"""Self-check for the non-streaming task response patch (issue #1600).

Bug: every task payload in open_webui/routers/tasks.py declares
'stream': False, and every in-process caller subscripts the result.
routers/openai.py decided on the RESPONSE content type alone, so when Hive's
edge-api answered a JWT session-chat request with text/event-stream (which it
does unconditionally, see apps/edge-api/internal/chat/dispatch.go), those
callers got a StreamingResponse. chat_web_search_handler subscripted it,
raised TypeError before extracting anything, and fell back to searching the
RAW USER MESSAGE instead of a query written for retrieval.

RED and GREEN are both proved here against the real shipped consumer rather
than a reimplementation of it: chat_web_search_handler is lifted out of the
vendored middleware.py with ast and executed against stubs, once fed the
shape the gateway produces today (a StreamingResponse) and once fed what the
patched producer returns. The first run is asserted to search the raw message,
the second to search the generated query. Reverting the fix fails the second
assertion on the QUERY ITSELF, not on the absence of an exception, which is
the whole point: a check that only asserted "no exception" would stay green
while search ran on the raw message forever.

Structural, no framework, no network.
Run: python3 scripts/test_owui_task_nonstreaming_response.py
"""

import ast
import asyncio
import builtins
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
PATCHES = REPO_ROOT / "deploy/docker/owui-patches"
PATCH = PATCHES / "apply_task_nonstreaming_response_1600_patch.py"
DIGEST = PATCHES / "pinned-openai-digest.json"
DOCKERFILE = REPO_ROOT / "deploy/docker/Dockerfile.open-webui"
VENDOR = REPO_ROOT / "vendor/open-webui/backend/open_webui"

# Deliberately unalike: a conversational question, and the query a task model
# would write for a search engine. An assertion that cannot tell them apart
# cannot tell this bug apart from its fix.
RAW_MESSAGE = "so what happened with that gateway pricing thing recently"
GENERATED_QUERY = "Hive gateway pricing change August 2026"


def sse_body(query):
    """The wire shape edge-api relays for a chat completion, split across frames.

    The generated query is deliberately cut in half across two delta frames,
    so a collapse that only read the first frame would produce a truncated
    query and fail the assertions below.
    """
    payload = json.dumps(dict(queries=[query]))
    half = len(payload) // 2
    frames = []
    frames.append(dict(id="chatcmpl-hive", created=1756600000, model="hive-auto", choices=[dict(index=0, delta=dict(role="assistant", content=""))]))
    frames.append(dict(id="chatcmpl-hive", choices=[dict(index=0, delta=dict(content=payload[:half]))]))
    frames.append(dict(id="chatcmpl-hive", choices=[dict(index=0, delta=dict(content=payload[half:]))]))
    frames.append(dict(id="chatcmpl-hive", choices=[dict(index=0, delta=dict(), finish_reason="stop")], usage=dict(prompt_tokens=11, completion_tokens=7, total_tokens=18)))
    return sse_frames(frames)


def sse_frames(frames):
    out = "".join("data: " + json.dumps(f) + "\n\n" for f in frames)
    return (out + "data: [DONE]\n\n").encode("utf-8")


def tool_call_sse_body():
    """A native tool call, fragmented the way a delta stream actually sends one.

    The arguments arrive in two pieces keyed by the same index, so a collapse
    that ignored tool calls, or that took only the last fragment, produces
    something that is not a valid OpenAI completion.
    """
    arguments = json.dumps(dict(query=GENERATED_QUERY))
    half = len(arguments) // 2
    return sse_frames(
        [
            dict(id="chatcmpl-tool", choices=[dict(index=0, delta=dict(role="assistant", tool_calls=[dict(index=0, id="call_1", type="function", function=dict(name="search_web", arguments=arguments[:half]))]))]),
            dict(id="chatcmpl-tool", choices=[dict(index=0, delta=dict(tool_calls=[dict(index=0, function=dict(arguments=arguments[half:]))]))]),
            dict(id="chatcmpl-tool", choices=[dict(index=0, delta=dict(), finish_reason="tool_calls")]),
        ]
    )


def error_frame_sse_body():
    """An SSE relay that fails after the headers are already on the wire.

    The response opened with HTTP 200, so the `r.status >= 400` check above the
    guard never sees this. Returning the partial text as a finished answer is
    the silent data loss the guard has to refuse.
    """
    return sse_frames(
        [
            dict(id="chatcmpl-hive", choices=[dict(index=0, delta=dict(content="partial "))]),
            dict(error=dict(message="upstream exploded", type="server_error")),
        ]
    )


def unfinished_sse_body():
    """Content, and no frame ever supplies a finish_reason."""
    return sse_frames(
        [
            dict(id="chatcmpl-hive", choices=[dict(index=0, delta=dict(content="partial answer"))]),
        ]
    )


class FakeUpstreamResponse:
    """The narrow slice of an aiohttp ClientResponse the helper touches."""

    def __init__(self, body):
        self._body = body
        self.reads = 0

    async def read(self):
        self.reads += 1
        return self._body


class RecordingLog:
    def __init__(self):
        self.warnings = []
        self.debugs = []
        self.exceptions = []

    def warning(self, msg, *args):
        self.warnings.append(msg % args if args else msg)

    def debug(self, msg, *args):
        self.debugs.append(msg % args if args else msg)

    def info(self, msg, *args):
        pass

    def error(self, msg, *args):
        pass

    def exception(self, err, *args):
        self.exceptions.append(str(err))


class FakeJSONResponse:
    """Stands in for starlette's JSONResponse in the isinstance check."""

    def __init__(self, body=b"", status_code=400):
        self.body = body
        self.status_code = status_code


class StreamingResponse:
    """The pre-fix shape, named so the TypeError message matches production.

    Nothing is stubbed about the failure: subscripting an object with no
    __getitem__ raises exactly what the demo box logged, which is
    'StreamingResponse' object is not subscriptable.
    """


def extract(source, name):
    for node in ast.parse(source).body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == name:
            return node
    return None


def module_bound_names(tree):
    """Names bound at MODULE scope, without descending into any function body.

    A name assigned inside some unrelated function is not readable from the
    splice point, so counting it here would be exactly the false green this
    check exists to prevent.
    """
    names = set()
    pending = list(tree.body)
    while pending:
        node = pending.pop()
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            names.add(node.name)
            continue
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            for alias in node.names:
                names.add((alias.asname or alias.name).split(".")[0])
            continue
        for child in ast.iter_child_nodes(node):
            if isinstance(child, ast.Name) and isinstance(child.ctx, ast.Store):
                names.add(child.id)
            else:
                pending.append(child)
    return names


def function_bound_names(node):
    """Parameters plus everything the function body binds."""
    names = set()
    args = node.args
    for arg in list(args.posonlyargs) + list(args.args) + list(args.kwonlyargs):
        names.add(arg.arg)
    if args.vararg:
        names.add(args.vararg.arg)
    if args.kwarg:
        names.add(args.kwarg.arg)
    for child in ast.walk(node):
        if isinstance(child, ast.Name) and isinstance(child.ctx, ast.Store):
            names.add(child.id)
        elif isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            names.add(child.name)
        elif isinstance(child, ast.ExceptHandler) and child.name:
            names.add(child.name)
        elif isinstance(child, (ast.Import, ast.ImportFrom)):
            for alias in child.names:
                names.add((alias.asname or alias.name).split(".")[0])
    return names


def unresolved_reads(node, bound):
    """Names the node READS that nothing in `bound` or builtins supplies.

    The structural checks below match the guard as a string and parse the
    result, and neither can resolve a name: an upstream bump that renamed
    `form_data`, `requested_model`, `json` or `log` would leave the marker
    count at 2, keep ast.parse happy, and surface only when the container
    serves a request. This turns that into a red check.
    """
    reads = {
        child.id
        for child in ast.walk(node)
        if isinstance(child, ast.Name) and isinstance(child.ctx, ast.Load)
    }
    return reads - bound - set(dir(builtins))


def load(node, namespace):
    """Execute one lifted function on its own, with a supplied namespace.

    Importing the module is not an option: it pulls in the whole Open WebUI
    backend, which is not installed here. This runs the REAL shipped code
    rather than a copy that could agree with a broken original.
    """
    exec(compile(ast.Module([node], []), "<lifted>", "exec"), namespace)
    return namespace[node.name]


def run_patch(dest_dir):
    dest = dest_dir / "openai.py"
    shutil.copy(VENDOR / "routers/openai.py", dest)
    env = dict(os.environ)
    env["HIVE_OWUI_OPENAI_PY"] = str(dest)
    result = subprocess.run([sys.executable, str(PATCH)], env=env, capture_output=True, text=True)
    return result, dest


class FakeRequestState:
    pass


class FakeRequest:
    def __init__(self):
        self.state = FakeRequestState()


class SearchForm:
    def __init__(self, queries):
        self.queries = queries


def run_web_search_handler(task_response):
    """Drive the REAL chat_web_search_handler with one stubbed task response.

    Returns (queries_reaching_the_search_call, logged_exception_messages).
    process_web_search returns None so the handler takes its short
    no-results branch; what this check is about is the argument, not what
    comes back.
    """
    node = extract((VENDOR / "utils/middleware.py").read_text(), "chat_web_search_handler")
    assert node is not None, "chat_web_search_handler not found in the vendored middleware.py"

    searched = []
    recording_log = RecordingLog()

    async def generate_queries(request, form_data, user):
        return task_response

    async def process_web_search(request, form, user=None):
        searched.append(list(form.queries))
        return None

    async def event_emitter(event):
        return None

    namespace = dict(
        Request=object,
        json=json,
        log=recording_log,
        JSONResponse=FakeJSONResponse,
        ENABLE_QUERIES_CACHE=False,
        generate_queries=generate_queries,
        process_web_search=process_web_search,
        SearchForm=SearchForm,
        get_last_user_message=lambda messages: RAW_MESSAGE,
    )
    handler = load(node, namespace)

    form_data = dict(model="hive-auto", messages=[dict(role="user", content=RAW_MESSAGE)])
    extra_params = dict(__event_emitter__=event_emitter, __chat_id__="chat-1")
    asyncio.run(handler(FakeRequest(), form_data, extra_params, object()))
    return searched, recording_log.exceptions


def check_pinned_digests(checks):
    """The vendored copies must still be what the backend image ships.

    The patch and the consumer are both exercised against vendor/, which is
    only evidence about the container while the two agree. See the note in
    pinned-openai-digest.json.
    """
    pinned = json.loads(DIGEST.read_text())

    # CodeRabbit, PR #1614: pinned["image"] was read and never used, so a
    # digest bump in the Dockerfile could leave these hashes describing a
    # backend the container no longer runs while this check stayed green.
    backend_from = [
        line[len("FROM "):].strip()
        for line in DOCKERFILE.read_text().splitlines()
        if line.startswith("FROM ghcr.io/open-webui/open-webui:")
    ]
    checks["the pinned digest names the image the Dockerfile builds on"] = (
        len(backend_from) == 1 and backend_from[0] == pinned["image"]
    )
    for image_path, expected in pinned["files"].items():
        relative = image_path.split("/app/backend/open_webui/", 1)[1]
        local = VENDOR / relative
        actual = hashlib.sha256(local.read_bytes()).hexdigest()
        checks["vendored " + relative + " still matches the pinned image"] = (
            actual == expected["sha256"] and local.stat().st_size == expected["bytes"]
        )


def main():
    checks = dict()
    check_pinned_digests(checks)

    # RED: the shape the gateway returns today, through the real consumer.
    searched, errors = run_web_search_handler(StreamingResponse())
    checks["RED: a streaming response makes the handler search the raw message"] = (
        searched == [[RAW_MESSAGE]]
    )
    checks["RED: it fails with the exact TypeError the demo box logged"] = any(
        "'StreamingResponse' object is not subscriptable" in message for message in errors
    )

    with tempfile.TemporaryDirectory() as tmp:
        result, dest = run_patch(Path(tmp))
        checks["patch script exits 0"] = result.returncode == 0
        if result.returncode != 0:
            print(result.stdout)
            print(result.stderr)
            print("FAIL: patch script did not run")
            return 1

        patched = dest.read_text()
        checks["patched module still parses"] = parses(patched)
        checks["marker count matches the Dockerfile assertion"] = (
            patched.count("# hive (#1600)") == 2
        )

        again = subprocess.run(
            [sys.executable, str(PATCH)],
            env=dict(os.environ, HIVE_OWUI_OPENAI_PY=str(dest)),
            capture_output=True,
            text=True,
        )
        checks["re-running the patch is a no-op"] = (
            again.returncode == 0 and dest.read_text() == patched
        )

        # The guard has to sit BEFORE the branch it overrides, or it is dead.
        guard = "if not form_data.get('stream'):\n                return await _hive_collapse_sse_completion(r, requested_model)\n\n            streaming = True"
        checks["the guard precedes the streaming branch it overrides"] = guard in patched
        checks["only the chat-completions branch is guarded"] = (
            patched.count("await _hive_collapse_sse_completion(") == 1
        )

        node = extract(patched, "_hive_collapse_sse_completion")
        checks["patch defines the collapse helper"] = node is not None
        if node is None:
            for name in (
                "GREEN: the collapsed response carries the generated query",
                "GREEN: the generated query, not the raw message, reaches the search call",
                "the collapse reads the upstream body exactly once",
                "usage from the terminal frame survives the collapse",
                "an unparseable body raises rather than fabricating a completion",
            ):
                checks[name] = False
            return report(checks)

        # Name resolution, which neither the string match nor ast.parse can do.
        module_names = module_bound_names(ast.parse(patched))
        route = extract(patched, "generate_chat_completion")
        guard_node = None
        if route is not None:
            for child in ast.walk(route):
                if isinstance(child, ast.If) and "_hive_collapse_sse_completion" in ast.dump(child):
                    guard_node = child
                    break
        checks["the guard is inside the chat-completions route"] = guard_node is not None
        checks["every name the guard reads resolves at the splice point"] = (
            guard_node is not None
            and unresolved_reads(guard_node, function_bound_names(route) | module_names) == set()
        )
        checks["every name the collapse helper reads resolves at module scope"] = (
            unresolved_reads(node, function_bound_names(node) | module_names) == set()
        )

        recording_log = RecordingLog()
        namespace = dict(json=json, log=recording_log, _HIVE_SSE_COLLAPSE_WARNED=False)
        collapse = load(node, namespace)

        upstream = FakeUpstreamResponse(sse_body(GENERATED_QUERY))
        collapsed = asyncio.run(collapse(upstream, "hive-auto"))

        # The exact expression chat_web_search_handler uses, on the real
        # collapsed object. This is what raised in production.
        content = collapsed["choices"][0]["message"]["content"]
        checks["GREEN: the collapsed response carries the generated query"] = (
            parse_json(content) == dict(queries=[GENERATED_QUERY])
        )
        checks["the collapse reads the upstream body exactly once"] = upstream.reads == 1
        checks["usage from the terminal frame survives the collapse"] = (
            collapsed.get("usage", dict()).get("total_tokens") == 18
        )
        checks["the first collapse warns that upstream ignored stream"] = any(
            "text/event-stream" in message for message in recording_log.warnings
        )
        # Three statements, not one `and` chain: short circuiting would skip
        # the later assertions entirely if the first one ever failed.
        checks["the first collapse warns exactly once"] = len(recording_log.warnings) == 1
        second = collapse_or_none(collapse, sse_body(GENERATED_QUERY))
        checks["a second collapse still returns a completion"] = second is not None
        checks["a second collapse does not repeat the warning"] = len(recording_log.warnings) == 1
        checks["a second collapse logs at debug instead"] = len(recording_log.debugs) == 1

        # Native tool calls survive, fragmented arguments and all. Dropping
        # them produced an empty assistant message wearing
        # finish_reason: tool_calls, which is not a valid completion.
        with_tools = collapse_or_none(collapse, tool_call_sse_body())
        checks["a tool-calling stream keeps its tool calls, arguments rejoined"] = (
            with_tools is not None
            and with_tools["choices"][0]["message"].get("tool_calls")
            == [
                dict(
                    id="call_1",
                    type="function",
                    function=dict(name="search_web", arguments=json.dumps(dict(query=GENERATED_QUERY))),
                )
            ]
        )
        checks["a tool-calling stream keeps finish_reason tool_calls"] = (
            with_tools is not None and with_tools["choices"][0]["finish_reason"] == "tool_calls"
        )

        # A stream that never finished is not reported as a clean stop.
        unfinished = collapse_or_none(collapse, unfinished_sse_body())
        checks["a stream that supplied no finish_reason does not invent one"] = (
            unfinished is not None and unfinished["choices"][0]["finish_reason"] is None
        )

        # Nothing usable RAISES. A fabricated empty completion reaches
        # chat_completion_files_handler as queries == [''] and makes retrieval
        # embed and search the empty string, which is worse than the
        # pre-patch TypeError it replaced.
        checks["an unparseable body raises rather than fabricating a completion"] = (
            collapse_or_none(collapse, b"not sse at all\n") is None
        )
        checks["an empty stream raises rather than fabricating a completion"] = (
            collapse_or_none(collapse, b"data: [DONE]\n\n") is None
        )
        checks["a mid-stream error frame raises rather than returning the partial answer"] = (
            collapse_or_none(collapse, error_frame_sse_body()) is None
        )
        checks["the discarded mid-stream error is named in the log"] = any(
            "upstream exploded" in message for message in recording_log.warnings
        )

        # GREEN, end to end: the real consumer, fed what the patched producer
        # now returns, searches the generated query.
        searched, errors = run_web_search_handler(collapsed)
        checks["GREEN: the generated query, not the raw message, reaches the search call"] = (
            searched == [[GENERATED_QUERY]] and searched != [[RAW_MESSAGE]]
        )
        checks["GREEN: the consumer logs no exception on the fixed shape"] = errors == []

    return report(checks)


def collapse_or_none(collapse, body):
    """Run one collapse, reporting a refusal as None rather than a crash.

    Same trade as the tolerant parse below: a raise where a completion was
    expected has to FAIL one check, not abort the run before the remaining
    checks report.
    """
    try:
        return asyncio.run(collapse(FakeUpstreamResponse(body), "hive-auto"))
    except Exception:
        return None


def parses(source):
    try:
        ast.parse(source)
        return True
    except SyntaxError:
        return False


def report(checks):
    failed = [name for name, ok in checks.items() if not ok]
    for name, ok in checks.items():
        print(("PASS  " if ok else "FAIL  ") + name)
    if failed:
        print()
        print("FAILED " + str(len(failed)) + " of " + str(len(checks)) + " checks")
        return 1
    print()
    print("OK " + str(len(checks)) + " checks (#1600)")
    return 0




def parse_json(text):
    """Tolerant parse: a truncated collapse must FAIL a check, not crash the run.

    A crash is loud but it stops every later check from reporting, which is
    how one broken assertion hides the rest of the suite.
    """
    try:
        return json.loads(text)
    except ValueError:
        return None


if __name__ == "__main__":
    sys.exit(main())
