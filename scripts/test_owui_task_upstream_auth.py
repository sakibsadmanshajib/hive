#!/usr/bin/env python3
"""Background task completions must carry the signed-in user's own credential.

Issue #1567. Every `/api/task/*` completion (title, tags, follow-ups,
autocomplete, image prompt, emoji, MOA, and both flavours of query generation)
reached edge-api under the static Open WebUI shim key with no
`__metadata.upstream_auth`, so `OWUIUnwrap` failed closed with 401 on
`/v1/chat/completions`, where `requiresPerUserAuth` is unconditional. Web
search was the symptom that got noticed; retrieval query generation, which is
the RAG half, broke the same way and behind the same silent fallback shape.

WHY THE CREDENTIAL WAS MISSING, which is the part worth pinning
---------------------------------------------------------------
`deploy/docker/pipelines/hive_jwt_forward.py` is a NATIVE Functions Filter. Open
WebUI runs that chain from `process_chat_payload` and `process_filter_functions`
only, which is the main chat path. `routers/tasks.py` runs
`process_pipeline_inlet_filter` instead, the legacy external Pipelines
mechanism, and then calls `utils/chat.py::generate_chat_completion` directly.
The Filter is never invoked on that path, so the injection never happens. The
gap is structural rather than conditional: no configuration, model, quota or
capability changes it.

WHAT IS ASSERTED
----------------
Behaviourally, both legs, against the real vendored source:

  * the PRE-FIX leg drives two different task handlers end to end and OBSERVES
    the payload arriving at the upstream forwarder with no `__metadata` at all,
    which is the 401 this issue is about;
  * the POST-FIX leg drives the same handlers and observes the payload carrying
    `Bearer <this user's access token>`;
  * the main chat path's already-injected credential is NOT displaced, so the
    seam cannot overwrite a token the Filter already resolved and does not pay
    for a second OAuth session lookup on the hot path;
  * a user with no resolvable OAuth session attaches nothing, so the request
    still fails closed at edge-api rather than being served under the shim's
    principal. This is the assertion that would go red if somebody "fixed" the
    401 by widening what the shim key may do;
  * the token is attached WITHOUT mutating the caller's payload, per the repo's
    immutability convention.

Structurally:

  * neither the helper nor the patch mentions the shim key or edge-api's
    per-user-auth requirement. Stated as an assertion because the tempting fix
    for this defect is to relax the boundary rather than to carry the
    credential, and this repo has already shipped one admin bypass that way
    (#1511);
  * every `/api/task/*` handler in `routers/tasks.py` still terminates in
    `utils/chat.py::generate_chat_completion`, so the single seam really is a
    seam and the fix cannot silently degrade into a search-only fix when
    upstream adds a ninth task type;
  * the Dockerfile applies the patch and pins its marker count.

No framework, no network, no database.
Run: python3 scripts/test_owui_task_upstream_auth.py
"""

import ast
import asyncio
import hashlib
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from test_owui_chat_delete_authz import vendored_and_pinned_versions  # noqa: E402

REPO_ROOT = Path(__file__).resolve().parents[1]
VENDORED_BACKEND = REPO_ROOT / "vendor/open-webui/backend/open_webui"
PATCHES = REPO_ROOT / "deploy/docker/owui-patches"
DOCKERFILE = REPO_ROOT / "deploy/docker/Dockerfile.open-webui"
UNWRAP_GO = REPO_ROOT / "apps/edge-api/internal/auth/owui_unwrap.go"
PINNED_CHAT_DIGEST = REPO_ROOT / "deploy/docker/owui-patches/pinned-chat-digest.json"

PATCH = "apply_task_upstream_auth_patch.py"
HELPER_MODULE = "hive_upstream_auth.py"
MARKER = "# hive (#1567)"
EXPECTED_MARKERS = 2

USER_ID = "user-42"
USER_EMAIL = "signed-in@example.invalid"
ACCESS_TOKEN = "access-token-for-the-signed-in-user"
CHAT_MODEL = "hive-free"

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok: {message}")
    else:
        failures.append(message)
        print(f"  FAIL: {message}")


def load_helper():
    """The Hive helper module, imported as a file rather than as a package.

    It is copied into the image at open_webui/utils/hive_upstream_auth.py, so
    there is no package to import it from here. Its only Open WebUI dependency
    is resolved lazily inside a function, precisely so this is possible.
    """
    path = PATCHES / HELPER_MODULE
    if not path.exists():
        raise SystemExit(f"FAIL: helper module missing: {path}")
    spec = importlib.util.spec_from_file_location("hive_upstream_auth", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def chat_util_source(apply_patch: bool) -> str:
    """utils/chat.py as the image runs it, with or without the #1567 patch."""
    with tempfile.TemporaryDirectory(prefix="owui-task-auth-") as tmpdir:
        tmp = Path(tmpdir)
        utils = tmp / "utils"
        utils.mkdir()
        shutil.copy(VENDORED_BACKEND / "utils/chat.py", utils / "chat.py")
        if apply_patch:
            patch_path = PATCHES / PATCH
            if not patch_path.exists():
                raise SystemExit(f"FAIL: patch missing: {patch_path}")
            env = dict(os.environ)
            env["HIVE_OWUI_BACKEND_DIR"] = str(tmp)
            result = subprocess.run(
                [sys.executable, str(patch_path)],
                env=env,
                capture_output=True,
                text=True,
            )
            if result.returncode != 0:
                raise SystemExit(f"FAIL: {PATCH} failed:\n{result.stdout}{result.stderr}")
        return (utils / "chat.py").read_text(encoding="utf-8")


class StubState:
    """request.state, which both modules probe with getattr/hasattr."""


class StubAppState:
    def __init__(self, models):
        self.MODELS = models


class StubApp:
    def __init__(self, models):
        self.state = StubAppState(models)


class StubRequest:
    def __init__(self, models):
        self.state = StubState()
        self.app = StubApp(models)
        self.cookies = {}


class StubUser:
    id = USER_ID
    email = USER_EMAIL
    role = "user"
    name = "Signed In"


class StubHTTPException(Exception):
    def __init__(self, status_code=None, detail=None):
        super().__init__(f"{status_code}: {detail}")
        self.status_code = status_code
        self.detail = detail


class Status:
    HTTP_200_OK = 200
    HTTP_400_BAD_REQUEST = 400
    HTTP_404_NOT_FOUND = 404


class ErrorMessages:
    @staticmethod
    def MODEL_NOT_FOUND(*args, **kwargs):
        return "model not found"

    @staticmethod
    def FEATURE_DISABLED(*args, **kwargs):
        return "feature disabled"


class Tasks:
    TITLE_GENERATION = "title_generation"
    QUERY_GENERATION = "query_generation"


class Log:
    @staticmethod
    def _noop(*args, **kwargs):
        return None

    debug = info = warning = error = exception = _noop


class Recorder:
    """Stands in for routers/openai.py, the last hop before edge-api."""

    def __init__(self):
        self.payloads: list[dict] = []

    async def generate_openai_chat_completion(self, request=None, form_data=None, user=None):
        self.payloads.append(form_data)
        return {"choices": [{"message": {"content": '{"queries": ["a"]}'}}]}

    def upstream_auth(self):
        if not self.payloads:
            return None
        metadata = self.payloads[-1].get("__metadata")
        if not isinstance(metadata, dict):
            return None
        return metadata.get("upstream_auth")


CONFIG_VALUES = {
    "task.title.enable": True,
    "task.query.search.enable": True,
    "task.query.retrieval.enable": True,
    "task.model.default": "",
    "task.model.external": "",
    "task.title.prompt_template": "",
    "task.query.prompt_template": "",
}


async def _async_noop(*args, **kwargs):
    return None


async def _template(template, messages, user):
    return "prompt"


async def _identity_inlet(request, payload, user, models):
    """No Pipelines microservice is deployed, so upstream returns the payload."""
    return payload


class _Config:
    @staticmethod
    async def get(key):
        return CONFIG_VALUES[key]


class _JSONResponse:
    def __init__(self, status_code=None, content=None):
        self.status_code = status_code
        self.content = content


def _compile_into(source: str, names, namespace: dict) -> None:
    """Lift the named top-level functions out and make them callable.

    Only those nodes are compiled, so neither module's imports nor its FastAPI
    router construction runs. Executing vendored code is deliberate and is the
    same trust assumption the image build already makes on this tree.
    """
    tree = ast.parse(source)
    wanted = []
    for node in tree.body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in names:
            node.decorator_list = []
            wanted.append(node)
    missing = set(names) - {node.name for node in wanted}
    if missing:
        raise SystemExit(f"FAIL: functions not found in source: {sorted(missing)}")
    module = ast.Module(body=wanted, type_ignores=[])
    ast.fix_missing_locations(module)
    exec(compile(module, "<lifted>", "exec"), namespace)


def compile_scenario(chat_source: str, helper, token):
    """The real task handlers wired to the real generate_chat_completion.

    Everything stubbed here is upstream Open WebUI machinery or the Open WebUI
    OAuth session store. The two task handlers, generate_chat_completion and the
    Hive helper are the genuine article.
    """
    recorder = Recorder()

    async def resolve(request, user):
        return "" if token is None else token

    helper.resolve_upstream_auth = resolve

    chat_ns = {
        "BYPASS_MODEL_ACCESS_CONTROL": False,
        "log": Log,
        "Request": object,
        "Any": object,
        "generate_openai_chat_completion": recorder.generate_openai_chat_completion,
        "generate_ollama_chat_completion": None,
        "generate_direct_chat_completion": None,
        "generate_function_chat_completion": None,
        "check_model_access": _async_noop,
        "hive_attach_upstream_auth": helper.attach_upstream_auth,
    }
    _compile_into(chat_source, ("generate_chat_completion",), chat_ns)

    tasks_source = (VENDORED_BACKEND / "routers/tasks.py").read_text(encoding="utf-8")
    tasks_ns = {
        "Request": object,
        "Depends": lambda dep: None,
        "get_verified_user": object(),
        "Config": _Config,
        "JSONResponse": _JSONResponse,
        "status": Status,
        "HTTPException": StubHTTPException,
        "ERROR_MESSAGES": ErrorMessages,
        "TASKS": Tasks,
        "log": Log,
        "get_task_model_id": lambda model_id, *args, **kwargs: model_id,
        "title_generation_template": _template,
        "query_generation_template": _template,
        "DEFAULT_TITLE_GENERATION_PROMPT_TEMPLATE": "title template",
        "DEFAULT_QUERY_GENERATION_PROMPT_TEMPLATE": "query template",
        "process_pipeline_inlet_filter": _identity_inlet,
        "generate_chat_completion": chat_ns["generate_chat_completion"],
    }
    _compile_into(tasks_source, ("generate_title", "generate_queries"), tasks_ns)

    return tasks_ns, chat_ns, recorder


def new_request():
    models = {CHAT_MODEL: {"id": CHAT_MODEL, "name": CHAT_MODEL, "owned_by": "openai"}}
    return StubRequest(models)


TASK_CASES = (
    ("title", {"model": CHAT_MODEL, "messages": [{"role": "user", "content": "hi"}]}),
    (
        "web_search query",
        {
            "model": CHAT_MODEL,
            "messages": [{"role": "user", "content": "hi"}],
            "prompt": "hi",
            "type": "web_search",
        },
    ),
    (
        "retrieval query",
        {
            "model": CHAT_MODEL,
            "messages": [{"role": "user", "content": "hi"}],
            "prompt": "hi",
            "type": "retrieval",
        },
    ),
)


def run_leg(label: str, chat_source: str, helper, token=ACCESS_TOKEN):
    """Drive three different task completions and report what reached the forwarder."""
    tasks_ns, _chat_ns, recorder = compile_scenario(chat_source, helper, token)
    observed = {}
    for name, form_data in TASK_CASES:
        handler = tasks_ns["generate_title"] if name == "title" else tasks_ns["generate_queries"]
        asyncio.run(handler(new_request(), form_data, StubUser()))
        observed[name] = recorder.upstream_auth()
    print(f"  [{label}] upstream_auth observed at the forwarder: {observed}")
    return observed, recorder


# --- real-import smoke test (LOW 1 from the PR review) -----------------------
#
# Everything above lifts functions out with `ast` and execs them in a synthetic
# namespace, which is the right way to isolate the logic but means no real
# `import open_webui.utils.chat` ever happens. So the import topology this fix
# depends on, a module-scope import of the helper into chat.py and a LAZY import
# of middleware inside the helper, was verified only by hand against the pinned
# image. A future edit hoisting that middleware import to module scope closes a
# cycle and crash-loops the chat container, and nothing in CI would have gone
# red first. An outage is not a loud failure.
#
# This reproduces the cycle's exact topology with the REAL helper file and
# minimal stand-ins for the two upstream modules that form the loop:
#
#     middleware -> chat -> hive_upstream_auth -> (lazily) middleware
#
# then really imports it, in a fresh interpreter. The mutation leg hoists the
# import the way the regression would and asserts the import DOES fail, so the
# check cannot pass vacuously.
CYCLE_STUBS = {
    "open_webui/__init__.py": "",
    "open_webui/utils/__init__.py": "",
    # Imports chat at module scope and defines the symbol the helper wants AFTER
    # that import, which is what upstream does and what makes the cycle bite: at
    # the moment the helper would be imported, this module is already in
    # sys.modules but has not bound the name yet.
    "open_webui/utils/middleware.py": (
        "from open_webui.utils.chat import generate_chat_completion  # noqa: F401\n"
        "\n\n"
        "async def get_system_oauth_token(request, user):\n"
        "    return {'access_token': 'stub'}\n"
    ),
    # Imports the helper at module scope, exactly as the #1567 splice does.
    "open_webui/utils/chat.py": (
        "from open_webui.utils.hive_upstream_auth import (\n"
        "    attach_upstream_auth as hive_attach_upstream_auth,\n"
        ")\n"
        "\n\n"
        "async def generate_chat_completion(request, form_data, user):\n"
        "    return await hive_attach_upstream_auth(request, form_data, user)\n"
    ),
}

# Inserted beside the helper's other module-scope imports, which is where such
# an edit would actually land. NOT prepended to the file: that lands above
# `from __future__ import annotations` and fails with a SyntaxError instead,
# which would make the mutation leg pass for the wrong reason and prove nothing
# about circular imports.
HOISTED_ANCHOR = "import logging\n"
HOISTED_IMPORT = "import logging\nfrom open_webui.utils.middleware import get_system_oauth_token\n"


def import_cycle_result(hoist_middleware_import: bool):
    """Really import the loop in a fresh interpreter. Returns (ok, stderr)."""
    with tempfile.TemporaryDirectory(prefix="owui-import-cycle-") as tmpdir:
        tmp = Path(tmpdir)
        for relative, body in CYCLE_STUBS.items():
            path = tmp / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(body, encoding="utf-8")

        helper = (PATCHES / HELPER_MODULE).read_text(encoding="utf-8")
        if hoist_middleware_import:
            if helper.count(HOISTED_ANCHOR) != 1:
                raise SystemExit(
                    f"FAIL: cannot place the mutation import in {HELPER_MODULE}: "
                    f"expected exactly one {HOISTED_ANCHOR!r}"
                )
            helper = helper.replace(HOISTED_ANCHOR, HOISTED_IMPORT)
        (tmp / "open_webui/utils/hive_upstream_auth.py").write_text(helper, encoding="utf-8")

        # A subprocess rather than importlib in this process: a fresh interpreter
        # has an empty sys.modules, so the cycle is exercised from a cold start
        # the way the container does it, not against whatever this test has
        # already imported.
        result = subprocess.run(
            [sys.executable, "-c", "import open_webui.utils.middleware"],
            cwd=tmp,
            capture_output=True,
            text=True,
        )
        return result.returncode == 0, result.stderr


def check_import_cycle() -> None:
    print("\nthe import topology, really imported")

    ok, stderr = import_cycle_result(hoist_middleware_import=False)
    detail = "" if ok else f": {stderr.strip()[-300:]}"
    check(
        ok,
        "middleware -> chat -> hive_upstream_auth imports cleanly, so the "
        f"helper's lazy middleware import closes no cycle{detail}",
    )

    # Mutation leg. Without it a green result above proves nothing: a stub set
    # that could never deadlock would pass just as happily.
    hoisted_ok, hoisted_stderr = import_cycle_result(hoist_middleware_import=True)
    check(
        not hoisted_ok and "partially initialized module" in hoisted_stderr,
        "and hoisting that import to module scope really does break the import, "
        "so this check cannot pass vacuously",
    )


def main() -> int:
    print("scripts/test_owui_task_upstream_auth.py")

    vendored, pinned = vendored_and_pinned_versions()
    check(
        vendored is not None and pinned is not None and vendored == pinned,
        f"vendored frontend v{vendored} matches the pinned backend image v{pinned}, "
        "so patching the vendored tree describes the source the image runs",
    )

    # A version match is a weaker claim than it reads as: it compares
    # package.json against a Dockerfile tag and says nothing about the one file
    # this patch actually rewrites. PR CI never builds Dockerfile.open-webui, so
    # without this the first sign of a drifted anchor is a failed deploy image
    # build. The digest was taken from the pinned image itself; comparing the
    # vendored file against it makes every other assertion in this suite a
    # statement about the source the container runs, at the cost of one hash
    # rather than a multi-gigabyte image pull on every pull request.
    fixture = json.loads(PINNED_CHAT_DIGEST.read_text(encoding="utf-8"))
    dockerfile_digest = DOCKERFILE.read_text(encoding="utf-8")
    check(
        fixture["image"] in dockerfile_digest,
        "the fixture pins the same image digest Dockerfile.open-webui builds from",
    )
    vendored_chat = (VENDORED_BACKEND / "utils/chat.py").read_bytes()
    check(
        hashlib.sha256(vendored_chat).hexdigest() == fixture["sha256"],
        "the vendored utils/chat.py is byte-identical to the pinned image's copy, "
        "so the patch is exercised against the source the container runs",
    )

    helper = load_helper()

    print("\npre-fix: the defect, observed")
    unpatched = chat_util_source(apply_patch=False)
    check(MARKER not in unpatched, "the vendored utils/chat.py carries no #1567 marker of its own")
    before, _ = run_leg("pre-fix", unpatched, helper)
    check(
        all(value is None for value in before.values()),
        "pre-fix, no task completion carries upstream_auth: this is the 401",
    )

    print("\npost-fix: the seam")
    patched = chat_util_source(apply_patch=True)
    check(
        patched.count(MARKER) == EXPECTED_MARKERS,
        f"the patch leaves exactly {EXPECTED_MARKERS} #1567 markers in utils/chat.py",
    )
    after, recorder = run_leg("post-fix", patched, helper)
    check(
        all(value == f"Bearer {ACCESS_TOKEN}" for value in after.values()),
        "post-fix, title, web-search query and RETRIEVAL query completions all "
        "carry this user's own bearer token to edge-api",
    )
    check(
        all("__metadata" in payload for payload in recorder.payloads),
        "the credential rides the __metadata carrier OWUIUnwrap already reads, so "
        "edge-api needs no change and the auth boundary is untouched",
    )

    print("\npost-fix: an already-injected credential is not displaced")
    _tasks_ns, chat_ns, recorder = compile_scenario(patched, helper, ACCESS_TOKEN)
    existing = "Bearer token-from-the-chat-inlet-filter"
    asyncio.run(
        chat_ns["generate_chat_completion"](
            new_request(),
            {"model": CHAT_MODEL, "messages": [], "__metadata": {"upstream_auth": existing}},
            StubUser(),
        )
    )
    check(
        recorder.upstream_auth() == existing,
        "the main chat path's Filter-injected token wins, so the seam neither "
        "displaces it nor pays for a second OAuth session lookup per turn",
    )

    print("\npost-fix: no resolvable session still fails closed")
    tasks_ns, _chat_ns, recorder = compile_scenario(patched, helper, None)
    asyncio.run(
        tasks_ns["generate_title"](
            new_request(),
            {"model": CHAT_MODEL, "messages": [{"role": "user", "content": "hi"}]},
            StubUser(),
        )
    )
    check(
        recorder.upstream_auth() is None,
        "a user with no OAuth session attaches nothing, so edge-api still 401s "
        "rather than the request being served under the shim's principal",
    )

    print("\npost-fix: the caller's payload is not mutated")
    _tasks_ns, chat_ns, recorder = compile_scenario(patched, helper, ACCESS_TOKEN)
    caller_payload = {"model": CHAT_MODEL, "messages": []}
    asyncio.run(chat_ns["generate_chat_completion"](new_request(), caller_payload, StubUser()))
    check(
        "__metadata" not in caller_payload,
        "the seam returns a new payload instead of mutating the caller's, per the "
        "repository's immutability convention",
    )
    check(
        recorder.upstream_auth() == f"Bearer {ACCESS_TOKEN}",
        "and the payload that actually left still carries the token",
    )

    check_import_cycle()

    print("\nthe auth boundary itself is untouched")
    helper_source = (PATCHES / HELPER_MODULE).read_text(encoding="utf-8")
    patch_source = (PATCHES / PATCH).read_text(encoding="utf-8")
    combined = helper_source + patch_source
    check(
        "OPENAI_API_KEY" not in combined and "OWUI_SHIM_KEY" not in combined,
        "neither the helper nor the patch reads the static shim key",
    )
    # The obvious alternative fix for this defect is to let the shim key satisfy
    # /v1/chat/completions on its own, which makes the 401 disappear by billing
    # and auditing every background completion against the shim's account. Pinned
    # from this test rather than only in prose, because the pull request that
    # takes that shortcut will not describe itself as taking it, and this repo
    # has already shipped one live admin bypass by relaxing an auth check
    # (#1511, fixed in 9916c6ec5).
    unwrap = UNWRAP_GO.read_text(encoding="utf-8")
    # The arms may GROW: issue #1718 added the two charged web tool routes for
    # the same reason the agent arm exists, so a shim-key call with no user
    # token is refused rather than billed to the shim account. What must not
    # change is that /v1/chat/completions stays unconditional, which is the
    # relaxation this check exists to catch.
    check(
        'func requiresPerUserAuth(path string) bool {\n'
        '\treturn path == "/v1/chat/completions" ||\n'
        '\t\tpath == "/v1/agent/tasks" ||\n'
        '\t\tstrings.HasPrefix(path, "/v1/agent/tasks/") ||\n'
        '\t\tstrings.HasPrefix(path, "/v1/tools/")\n'
        "}" in unwrap,
        "edge-api still requires a per-user token on /v1/chat/completions "
        "unconditionally: this fix supplies the credential, it does not widen "
        "who may go without one (#1511)",
    )
    check(
        "\treturn rest == shimKey\n" in unwrap,
        "and the shim key is still matched exactly, so it gained no new reach",
    )

    print("\nthe seam covers every task type")
    tasks_tree = ast.parse((VENDORED_BACKEND / "routers/tasks.py").read_text(encoding="utf-8"))
    completion_handlers = []
    for node in tasks_tree.body:
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for decorator in node.decorator_list:
            if (
                isinstance(decorator, ast.Call)
                and isinstance(decorator.func, ast.Attribute)
                and decorator.args
                and isinstance(decorator.args[0], ast.Constant)
                and str(decorator.args[0].value).endswith("/completions")
            ):
                completion_handlers.append(node)
                break
    check(
        len(completion_handlers) >= 8,
        f"routers/tasks.py exposes {len(completion_handlers)} */completions handlers "
        "(upstream ships eight)",
    )
    unrouted = [
        node.name
        for node in completion_handlers
        if not any(
            isinstance(call, ast.Call)
            and isinstance(call.func, ast.Name)
            and call.func.id == "generate_chat_completion"
            for call in ast.walk(node)
        )
    ]
    check(
        not unrouted,
        "every one of them dispatches through utils/chat.py::generate_chat_completion, "
        f"so one seam covers them all (unrouted: {unrouted})",
    )

    print("\nthe image applies it")
    dockerfile = DOCKERFILE.read_text(encoding="utf-8")
    check(PATCH in dockerfile, f"Dockerfile.open-webui runs {PATCH}")
    check(HELPER_MODULE in dockerfile, f"Dockerfile.open-webui copies {HELPER_MODULE} in")
    check(
        f"grep -c '{MARKER}'" in dockerfile,
        "the Dockerfile pins the #1567 marker count, so a digest bump that drops "
        "the patch fails the build instead of shipping a silent 401",
    )

    print()
    if failures:
        print(f"FAILED ({len(failures)}):")
        for failure in failures:
            print(f"  - {failure}")
        return 1
    print("all checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
