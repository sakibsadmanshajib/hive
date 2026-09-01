#!/usr/bin/env python3
"""Hive's internal `__metadata` carrier must not cross to a vendor connection.

Issue #1578. `__metadata` is a Hive invention, not an Open WebUI or OpenAI
field. It carries the signed-in user's Supabase bearer token to edge-api's
`OWUIUnwrap` middleware, because Open WebUI puts one static shim key on
Authorization and offers no per-user header. Two things write it:
`deploy/docker/pipelines/hive_jwt_forward.py` on the main chat path and
`deploy/docker/owui-patches/hive_upstream_auth.py` at the task dispatch seam
(#1567). Nothing took it back off.

`routers/openai.py::generate_chat_completion` pops the upstream `metadata` key
and leaves `__metadata` in the forwarded payload, so it travels to whichever
OpenAI-compatible connection owns the resolved model. One administrator
setting makes that a third party: point the external task model
(`task.model.external`) at a model served by a second, vendor connection and
every conversation ships the user's bearer token to that vendor. No user
action, no second misconfiguration.

WHAT IS ASSERTED
----------------
Behaviourally, both legs, against the real vendored source, with TWO
OpenAI-compatible connections configured (Hive's own gateway and a vendor):

  * the PRE-FIX leg drives a completion at the vendor connection and OBSERVES
    the bearer token arriving in the outbound body. That leg is the "remove the
    strip and watch it go red" check issue #1578 asks for, run every time
    rather than by hand: it executes the unpatched vendored source, which is
    exactly the fix removed;
  * the POST-FIX leg drives the same completion and observes no `__metadata`
    at all in the vendor's body, while the same completion to Hive's own
    gateway still carries it byte for byte, so the #1567 credential path and
    edge-api's fail-closed 401 are untouched;
  * the WHOLE object goes, not `upstream_auth` alone. A Hive-internal carrier
    crossing a vendor boundary is the defect; the token is its worst payload,
    not its only one;
  * everything that is not `__metadata` survives, so this is a strip and not a
    payload rewrite;
  * the caller's own payload is not mutated, per the repository's immutability
    convention;
  * `embeddings` and `responses` serialise their bodies correctly after the
    move. Both used to serialise before the connection was resolved, which is
    why the patch moves those two lines rather than adding a call; a botched
    move is a NameError at runtime that no structural check would see.

At the unit level, on the helper itself:

  * with `OPENAI_API_BASE_URL` unset, NOTHING is a Hive gateway, so the carrier
    is dropped. Absence fails closed;
  * a trailing slash or surrounding whitespace resolves to the same connection,
    so a cosmetic difference in configuration cannot break a working
    deployment, while upstream's plural `OPENAI_API_BASE_URLS` is NOT trusted,
    because it is upstream's multi-connection form and honouring it would mean
    trusting every connection listed in it;
  * the warning line names the dropped FIELDS and a redacted destination, and
    contains neither the token nor any userinfo from the connection URL. A
    guard against credential egress that logs the credential has moved the
    leak, not closed it.

Structurally:

  * `routers/openai.py` makes exactly five requests carrying a body and has
    exactly five sanitiser call sites, so `speech` and `proxy`, which this file
    does not drive end to end, cannot be the hole;
  * the Dockerfile installs the helper, applies the patch and pins its marker
    count, so the fix reaches the running container rather than only the
    vendored tree, which the image does not run for the backend;
  * `make test-scripts` runs this file, which is what makes it a required check.

No framework, no network, no database. Every credential in here is an obvious
synthetic literal.
Run: python3 scripts/test_owui_internal_metadata_boundary.py
"""

import ast
import asyncio
import importlib.util
import io
import json
import logging
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
MAKEFILE = REPO_ROOT / "Makefile"

PATCH = "apply_internal_metadata_boundary_1578_patch.py"
HELPER_MODULE = "hive_internal_metadata.py"
MARKER = "# hive (#1578)"
EXPECTED_MARKERS = 6

# docker-compose.yml points the chat container's one OpenAI-compatible
# connection here. Everything else is somebody else's server.
GATEWAY_URL = "http://edge-api:8080/v1"
VENDOR_URL = "https://api.some-vendor.invalid/v1"

HIVE_MODEL = "hive-free"
VENDOR_MODEL = "vendor-task-model"

# Obvious synthetic values. Nothing in this file is, or resembles, a real
# credential.
FAKE_TOKEN = "Bearer SYNTHETIC-NOT-A-REAL-TOKEN-1578"
FAKE_CHAT_ID = "chat-0000-synthetic"
# The redaction leg needs a connection URL of the shape
# scheme://user:password@host/path?query. Held as parts and assembled at the
# call site, so the file contains no basic-auth literal for secret scanning to
# flag; the first pass of this pull request did, and GitGuardian was right to
# open an incident on it even though the value was invented.
FAKE_URL_USER = "operator"
FAKE_URL_PASSWORD = "SYNTHETIC-URL-PASSWORD"
FAKE_URL_QUERY_VALUE = "SYNTHETIC-QUERY-VALUE"

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok: {message}")
    else:
        failures.append(message)
        print(f"  FAIL: {message}")


def load_helper():
    """The Hive helper, imported as a file rather than as a package.

    It is copied into the image at open_webui/utils/hive_internal_metadata.py,
    so there is no package to import it from here. It depends on nothing from
    Open WebUI, which is what makes that possible.
    """
    path = PATCHES / HELPER_MODULE
    if not path.exists():
        raise SystemExit(f"FAIL: helper module missing: {path}")
    spec = importlib.util.spec_from_file_location("hive_internal_metadata", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def router_source(apply_patch: bool) -> str:
    """routers/openai.py as the image runs it, with or without the #1578 patch."""
    with tempfile.TemporaryDirectory(prefix="owui-metadata-boundary-") as tmpdir:
        tmp = Path(tmpdir)
        routers = tmp / "routers"
        routers.mkdir()
        shutil.copy(VENDORED_BACKEND / "routers/openai.py", routers / "openai.py")
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
        return (routers / "openai.py").read_text(encoding="utf-8")


# --- the stubs -------------------------------------------------------------
#
# Everything stubbed here is upstream Open WebUI machinery or aiohttp. The
# router functions under test and the Hive helper are the genuine article.


class StubState:
    pass


class StubAppState:
    def __init__(self, models):
        self.OPENAI_MODELS = models


class StubApp:
    def __init__(self, models):
        self.state = StubAppState(models)


class StubURL:
    query = ""


class StubRequest:
    def __init__(self, models, body=b""):
        self.state = StubState()
        self.app = StubApp(models)
        self.cookies = {}
        self.method = "POST"
        self.url = StubURL()
        self._body = body

    async def body(self):
        return self._body


class StubUser:
    id = "user-42"
    email = "signed-in@example.invalid"
    role = "user"
    name = "Signed In"


class StubResponsesForm:
    """Stands in for the pydantic ResponsesForm, which allows extra fields."""

    def __init__(self, payload):
        self._payload = payload
        self.model = payload.get("model")

    def model_dump(self, exclude_none=True):
        return dict(self._payload)


class StubHTTPException(Exception):
    def __init__(self, status_code=None, detail=None):
        super().__init__(f"{status_code}: {detail}")
        self.status_code = status_code
        self.detail = detail


class Status:
    HTTP_403_FORBIDDEN = 403
    HTTP_404_NOT_FOUND = 404


class ErrorMessages:
    MODEL_NOT_FOUND = staticmethod(lambda *a, **k: "model not found")
    SERVER_CONNECTION_ERROR = "server connection error"
    ACCESS_PROHIBITED = "access prohibited"


class Log:
    """Loud on the way out.

    generate_chat_completion wraps its whole request in `except Exception` and
    re-raises an HTTPException, so a mistake in these stubs would otherwise
    surface as a generic 500 with the real cause swallowed, and a test could go
    red for a reason nobody could see.
    """

    @staticmethod
    def _noop(*args, **kwargs):
        return None

    debug = info = warning = error = _noop

    @staticmethod
    def exception(*args, **kwargs):
        print(f"  (router raised: {args[0]!r})", file=sys.stderr)


class StubModels:
    @staticmethod
    async def get_model_by_id(model_id):
        return None


class StubResponse:
    status = 200
    headers = {"Content-Type": "application/json"}

    async def json(self):
        return {"choices": [{"message": {"content": "ok"}}]}

    async def text(self):
        return "ok"


class Recorder:
    """Stands in for aiohttp: the last hop before the wire."""

    def __init__(self):
        self.sent: list[tuple[str, object]] = []

    async def request(self, method=None, url=None, data=None, **kwargs):
        self.sent.append((url, data))
        return StubResponse()

    async def post(self, url=None, data=None, **kwargs):
        self.sent.append((url, data))
        return StubResponse()

    def body_for(self, host_fragment: str):
        """The decoded JSON body of the request sent to a given destination."""
        for url, data in self.sent:
            if host_fragment in url:
                if isinstance(data, bytes):
                    data = data.decode()
                return json.loads(data)
        raise AssertionError(f"no request was sent to {host_fragment}: {self.sent}")


class StubAiohttp:
    @staticmethod
    def ClientTimeout(**kwargs):
        return None


async def _async_noop(*args, **kwargs):
    return None


def _compile_into(source: str, names, namespace: dict) -> None:
    """Lift the named top-level functions out and make them callable.

    Only those nodes are compiled, so neither the module's imports nor its
    FastAPI router construction runs. Executing vendored code is deliberate and
    is the same trust assumption the image build already makes on this tree.
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


CONNECTIONS = {0: GATEWAY_URL, 1: VENDOR_URL}
MODELS = {
    HIVE_MODEL: {"id": HIVE_MODEL, "name": HIVE_MODEL, "urlIdx": 0},
    VENDOR_MODEL: {"id": VENDOR_MODEL, "name": VENDOR_MODEL, "urlIdx": 1},
}


def compile_router(source: str, helper):
    """The real router functions, wired to a recorder in place of aiohttp."""
    recorder = Recorder()

    async def get_openai_connection(idx):
        return CONNECTIONS[idx], "hk_synthetic_shim_key", {}

    async def get_session():
        return recorder

    namespace = {
        "json": json,
        "re": __import__("re"),
        "aiohttp": StubAiohttp,
        "log": Log,
        # `from __future__ import annotations` is not carried over by
        # _compile_into, so parameter annotations are evaluated at def time and
        # every name in one has to exist here.
        "Request": object,
        "ResponsesForm": StubResponsesForm,
        "Depends": lambda dep: None,
        "get_verified_user": object(),
        "BYPASS_MODEL_ACCESS_CONTROL": False,
        "AIOHTTP_CLIENT_TIMEOUT": 1,
        "AIOHTTP_CLIENT_SESSION_SSL": None,
        "ENABLE_OPENAI_API_PASSTHROUGH": True,
        "HTTPException": StubHTTPException,
        "status": Status,
        "ERROR_MESSAGES": ErrorMessages,
        "Models": StubModels,
        "check_model_access": _async_noop,
        "get_all_models": _async_noop,
        "get_openai_connection": get_openai_connection,
        "get_headers_and_cookies": _make_headers,
        "get_session": get_session,
        "cleanup_response": _async_noop,
        "is_openai_new_model": lambda model: False,
        "openai_reasoning_model_handler": lambda payload: payload,
        "convert_logit_bias_input_to_json": lambda value: None,
        "convert_to_azure_payload": lambda url, payload, api_version: (url, payload),
        "convert_to_responses_payload": lambda payload: payload,
        "convert_responses_result": lambda response: response,
        "publish_model_provider_request_failed": _async_noop,
        "stream_wrapper": lambda *a, **k: None,
        "stream_chunks_handler": None,
        "_clean_proxy_headers": lambda headers: {},
        "StreamingResponse": lambda *a, **k: None,
        "JSONResponse": lambda *a, **k: None,
        "PlainTextResponse": lambda *a, **k: None,
        "_sanitize_model_for_url": lambda model: model,
        # Present only once the patch has been applied; harmless before it.
        "hive_strip_internal_metadata": helper.strip_internal_metadata,
        "hive_strip_internal_metadata_body": helper.strip_internal_metadata_body,
    }
    _compile_into(source, ("generate_chat_completion", "embeddings", "responses", "proxy"), namespace)
    return namespace, recorder


async def _make_headers(request, url, key, api_config, metadata=None, user=None):
    return {}, {}


def new_request(body=b""):
    return StubRequest(MODELS, body=body)


def completion_payload(model: str) -> dict:
    """A chat completion carrying the internal carrier, as both writers leave it.

    `chat_id` is in there deliberately: issue #1578 asks that the audit cover
    every field the carrier holds, not the token alone.
    """
    return {
        "model": model,
        "messages": [{"role": "user", "content": "summarise this chat"}],
        "__metadata": {"upstream_auth": FAKE_TOKEN, "chat_id": FAKE_CHAT_ID},
    }


def drive_completion(source: str, helper, model: str):
    namespace, recorder = compile_router(source, helper)
    payload = completion_payload(model)
    asyncio.run(namespace["generate_chat_completion"](new_request(), payload, StubUser()))
    return recorder, payload


def gateway_env(extra=None):
    """The chat container's environment, as docker-compose.yml sets it."""
    env = {"OPENAI_API_BASE_URL": GATEWAY_URL}
    if extra:
        env.update(extra)
    return env


def main() -> int:
    print("scripts/test_owui_internal_metadata_boundary.py")

    vendored, pinned = vendored_and_pinned_versions()
    check(
        vendored is not None and pinned is not None and vendored == pinned,
        f"vendored frontend v{vendored} matches the pinned backend image v{pinned}, "
        "so patching the vendored tree describes the source the image runs",
    )

    helper = load_helper()
    # The helper's own warnings are asserted on further down, with a handler
    # attached for that block only. Everywhere else they are just noise on a
    # green run, and logging's last-resort handler would put every one of them
    # on stderr.
    helper.log.addHandler(logging.NullHandler())
    helper.log.propagate = False
    # The helper reads os.environ when no explicit mapping is passed, and the
    # router calls it with one argument, so the process environment is the
    # deployment's configuration for the duration of these legs.
    os.environ["OPENAI_API_BASE_URL"] = GATEWAY_URL
    os.environ.pop("OPENAI_API_BASE_URLS", None)

    unpatched = router_source(apply_patch=False)
    patched = router_source(apply_patch=True)

    check(MARKER not in unpatched, "the vendored routers/openai.py carries no #1578 marker of its own")
    check(
        patched.count(MARKER) == EXPECTED_MARKERS,
        f"the patch leaves exactly {EXPECTED_MARKERS} #1578 markers in routers/openai.py",
    )

    print("\npre-fix: the defect, observed at a vendor connection")
    before, _ = drive_completion(unpatched, helper, VENDOR_MODEL)
    leaked = before.body_for("some-vendor.invalid").get("__metadata", {})
    check(
        leaked.get("upstream_auth") == FAKE_TOKEN,
        "pre-fix, the signed-in user's bearer token reaches the vendor connection: "
        "this is issue #1578, and it is what goes red if the strip is removed",
    )

    print("\npost-fix: nothing internal reaches the vendor")
    after, caller_payload = drive_completion(patched, helper, VENDOR_MODEL)
    vendor_body = after.body_for("some-vendor.invalid")
    check(
        "__metadata" not in vendor_body,
        "post-fix, the whole __metadata carrier is gone from the vendor's payload, "
        "not just its upstream_auth field",
    )
    check(
        FAKE_TOKEN not in json.dumps(vendor_body),
        "and the token appears nowhere else in what the vendor receives",
    )
    check(
        FAKE_CHAT_ID not in json.dumps(vendor_body),
        "nor does any other field the internal carrier held",
    )
    check(
        vendor_body.get("model") == VENDOR_MODEL
        and vendor_body.get("messages") == [{"role": "user", "content": "summarise this chat"}],
        "while every field that is not the carrier still reaches the vendor, so "
        "this is a strip and not a payload rewrite",
    )
    check(
        caller_payload.get("__metadata", {}).get("upstream_auth") == FAKE_TOKEN,
        "and the caller's own payload is left intact, per the repository's "
        "immutability convention",
    )

    print("\npost-fix: Hive's own gateway is unaffected")
    hive_run, _ = drive_completion(patched, helper, HIVE_MODEL)
    hive_body = hive_run.body_for("edge-api")
    check(
        hive_body.get("__metadata") == {"upstream_auth": FAKE_TOKEN, "chat_id": FAKE_CHAT_ID},
        "the carrier reaches edge-api byte for byte, so the #1567 credential path "
        "and OWUIUnwrap's fail-closed 401 are untouched",
    )

    print("\npost-fix: the two moved serialisations still serialise")
    namespace, recorder = compile_router(patched, helper)
    asyncio.run(
        namespace["embeddings"](
            new_request(),
            {"model": VENDOR_MODEL, "input": "x", "__metadata": {"upstream_auth": FAKE_TOKEN}},
            StubUser(),
        )
    )
    check(
        "__metadata" not in recorder.body_for("some-vendor.invalid"),
        "embeddings serialises its body after the connection is resolved and the "
        "carrier does not reach the vendor",
    )

    namespace, recorder = compile_router(patched, helper)
    asyncio.run(
        namespace["responses"](
            new_request(),
            StubResponsesForm(
                {"model": VENDOR_MODEL, "input": "x", "__metadata": {"upstream_auth": FAKE_TOKEN}}
            ),
            StubUser(),
        )
    )
    check(
        "__metadata" not in recorder.body_for("some-vendor.invalid"),
        "responses serialises its body after the connection is resolved and the "
        "carrier does not reach the vendor",
    )

    namespace, recorder = compile_router(patched, helper)
    passthrough = json.dumps(
        {"model": VENDOR_MODEL, "input": "x", "__metadata": {"upstream_auth": FAKE_TOKEN}}
    ).encode()
    asyncio.run(namespace["proxy"]("chat/completions", new_request(body=passthrough), StubUser()))
    check(
        "__metadata" not in recorder.body_for("some-vendor.invalid"),
        "the default-disabled passthrough proxy strips it too, so a caller cannot "
        "hand-carry the field past the boundary",
    )

    print("\nthe helper: absence fails closed")
    check(
        helper.strip_internal_metadata({"__metadata": {"upstream_auth": FAKE_TOKEN}}, GATEWAY_URL, {})
        == {},
        "with no gateway variable configured, even Hive's own URL is not "
        "recognised and the carrier is dropped",
    )
    check(
        helper.is_hive_gateway(GATEWAY_URL, gateway_env()) is True
        and helper.is_hive_gateway(VENDOR_URL, gateway_env()) is False,
        "with OPENAI_API_BASE_URL set, the gateway is recognised and a vendor is not",
    )
    check(
        helper.is_hive_gateway(GATEWAY_URL + "/", gateway_env({"OPENAI_API_BASE_URL": f" {GATEWAY_URL} "}))
        is True,
        "a trailing slash or surrounding whitespace on either side is the same "
        "connection, so cosmetic configuration drift cannot start forwarding",
    )
    check(
        helper.is_hive_gateway(
            VENDOR_URL,
            {"OPENAI_API_BASE_URLS": f"{GATEWAY_URL};{VENDOR_URL}"},
        )
        is False
        and helper.is_hive_gateway(
            GATEWAY_URL,
            {"OPENAI_API_BASE_URLS": f"{GATEWAY_URL};{VENDOR_URL}"},
        )
        is False,
        "upstream's plural OPENAI_API_BASE_URLS is NOT trusted: it is upstream's "
        "multi-connection form, so honouring it would mean trusting every "
        "connection an operator listed in it",
    )
    check(
        helper.is_hive_gateway(f"{GATEWAY_URL.upper()}", gateway_env()) is False,
        "a destination that differs from the configured gateway in any way other "
        "than trailing slash or whitespace, letter case included, is somebody "
        "else's and the carrier is dropped",
    )
    check(
        helper.is_hive_gateway("", gateway_env()) is False
        and helper.is_hive_gateway(None, gateway_env()) is False,
        "an empty or missing destination is never a Hive gateway",
    )

    print("\nthe helper: identity, non-dicts and raw bodies")
    untouched = {"model": HIVE_MODEL}
    check(
        helper.strip_internal_metadata(untouched, VENDOR_URL, gateway_env()) is untouched,
        "a payload with no carrier is returned as the same object, so nothing is "
        "re-serialised for the sake of it",
    )
    check(
        helper.strip_internal_metadata("not a dict", VENDOR_URL, gateway_env()) == "not a dict",
        "a payload that is not a dict is returned unchanged rather than raising",
    )
    raw = b"this is not json"
    check(
        helper.strip_internal_metadata_body(raw, VENDOR_URL, gateway_env()) is raw,
        "a non-JSON raw body is relayed verbatim",
    )
    carrier_body = json.dumps({"a": 1, "__metadata": {"upstream_auth": FAKE_TOKEN}}).encode()
    check(
        json.loads(helper.strip_internal_metadata_body(carrier_body, VENDOR_URL, gateway_env()))
        == {"a": 1},
        "a raw JSON body carrying the field is rebuilt without it",
    )

    print("\nthe helper: the warning does not become the leak")
    stream = io.StringIO()
    handler = logging.StreamHandler(stream)
    helper.log.addHandler(handler)
    helper.log.setLevel(logging.WARNING)
    try:
        helper.strip_internal_metadata(
            {"__metadata": {"upstream_auth": FAKE_TOKEN, "chat_id": FAKE_CHAT_ID}},
            # Assembled from parts rather than written as one literal. A base
            # URL of the shape scheme://user:password@host is what this leg has
            # to exercise, and spelling it out inline is what secret scanning is
            # supposed to flag, so it is not spelled out inline.
            f"https://{FAKE_URL_USER}:{FAKE_URL_PASSWORD}@api.some-vendor.invalid"
            f"/v1?key={FAKE_URL_QUERY_VALUE}",
            gateway_env(),
        )
    finally:
        helper.log.removeHandler(handler)
    logged = stream.getvalue()
    check(
        "upstream_auth" in logged and "chat_id" in logged,
        "the warning names the dropped fields, so an operator can see what was "
        "carried without reading the values",
    )
    check(
        FAKE_TOKEN not in logged
        and FAKE_URL_PASSWORD not in logged
        and FAKE_URL_QUERY_VALUE not in logged,
        "and it contains neither the token, nor userinfo from the connection URL, "
        "nor its query string: a guard that logs the credential has moved the leak",
    )
    check(
        "api.some-vendor.invalid" in logged,
        "while still naming the destination host, which is what makes the line "
        "actionable",
    )

    print("\nthe boundary is the whole boundary")
    check(
        patched.count("data=payload,") + patched.count("data=body,") == 5
        and patched.count("hive_strip_internal_metadata(")
        + patched.count("hive_strip_internal_metadata_body(")
        == 5,
        "routers/openai.py sends exactly five bodies and has exactly five "
        "sanitiser call sites, so speech and proxy are covered too and a sixth "
        "added upstream fails the patch rather than becoming a hole",
    )
    check(
        "body = json.dumps(form_data)\n" not in patched
        and "body = json.dumps(payload)\n" not in patched,
        "neither body is serialised before its connection is resolved any more, so "
        "no sanitiser is comparing against a destination it does not know",
    )

    print("\nthe fix reaches the running container")
    dockerfile = DOCKERFILE.read_text(encoding="utf-8")
    check(
        f"COPY deploy/docker/owui-patches/{HELPER_MODULE} "
        f"/app/backend/open_webui/utils/{HELPER_MODULE}" in dockerfile,
        "the Dockerfile installs the helper into the backend the image runs; the "
        "vendored tree supplies only the frontend, so a vendored edit would ship "
        "nothing",
    )
    check(
        f"RUN python3 /tmp/{PATCH}" in dockerfile,
        "and applies the patch at image build time",
    )
    check(
        f"'{MARKER}' /app/backend/open_webui/routers/openai.py)\" -eq {EXPECTED_MARKERS}" in dockerfile,
        f"and pins the marker count at {EXPECTED_MARKERS}, so a digest bump that "
        "drops the fix fails the build instead of reopening the egress",
    )
    check(
        "test_owui_internal_metadata_boundary.py" in MAKEFILE.read_text(encoding="utf-8"),
        "make test-scripts runs this file, which is what makes it a required check",
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
