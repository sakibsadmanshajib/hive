#!/usr/bin/env python3
"""A chat embedding must be billed to the user who caused it.

Issue #1696. Open WebUI's Python retrieval path posts to Hive's own gateway with
`RAG_OPENAI_API_KEY`, which on this deployment is `OWUI_SHIM_KEY`. Web search
indexing, document ingest and retrieval queries therefore all spent real metered
credits against one shared platform account: the searching customer's usage
showed nothing, one account absorbed every tenant's embedding spend at once, and
the per-tenant budget never saw the request it was supposed to cap.

WHAT IS ASSERTED

Behaviourally, against the real module the image runs:

  * a call for a signed-in user carries `X-Hive-Upstream-Auth: Bearer <that
    user's access token>`, which is the carrier edge-api reads a per-user
    principal from;
  * a call with no resolvable user REFUSES rather than going out under the
    shim key. This is the assertion that would go red if somebody "fixed" a
    future 401 by letting the shared key through again, which is the defect
    itself;
  * a call to anything but the gateway named in the ENVIRONMENT refuses before
    a credential is even resolved. The destination is admin-writable persistent
    config, so without this the change would trade a leaked shared platform key
    for a harvest of per-user session bearers;
  * the two producers that used to embed with no user (knowledge base metadata,
    and the knowledge base search builtin) now carry one, so the new refusal
    does not break knowledge base ingest inside a swallowed except;
  * the caller's header dict is not mutated, per the repository's immutability
    convention;
  * the token cache does not leak one user's credential to another, and a
    failed resolution is not cached.

Structurally:

  * the splice lands inside `agenerate_openai_batch_embeddings` and BEFORE the
    upstream POST, so it cannot become a no-op that runs after the request;
  * `generate_embeddings` still dispatches to that function, so the one place
    patched really is the chokepoint;
  * `requiresPerUserAuth` in edge-api lists `/v1/embeddings`. The two halves
    are useless apart: without the Go half a missing carrier silently bills the
    shim again, and without the Python half every embedding 401s;
  * the Dockerfile applies the patch and pins its marker count;
  * the vendored tree and the pinned image agree on version, so patching the
    vendored copy here describes the source the image actually runs.

No framework, no network, no database.
Run: python3 scripts/test_owui_embed_attribution.py
"""

import ast
import asyncio
import importlib.util
import os
import re
import subprocess
import sys
import tempfile
import types
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from test_owui_chat_delete_authz import vendored_and_pinned_versions  # noqa: E402

REPO_ROOT = Path(__file__).resolve().parents[1]
VENDORED_UTILS = REPO_ROOT / "vendor/open-webui/backend/open_webui/retrieval/utils.py"
VENDORED_KNOWLEDGE = REPO_ROOT / "vendor/open-webui/backend/open_webui/routers/knowledge.py"
VENDORED_BUILTIN = REPO_ROOT / "vendor/open-webui/backend/open_webui/tools/builtin.py"
PATCHES = REPO_ROOT / "deploy/docker/owui-patches"
PATCH_SCRIPT = PATCHES / "apply_embed_attribution_1696_patch.py"
MODULE = PATCHES / "hive_embed_attribution.py"
DOCKERFILE = REPO_ROOT / "deploy/docker/Dockerfile.open-webui"
UNWRAP_GO = REPO_ROOT / "apps/edge-api/internal/auth/owui_unwrap.go"
COMPOSE = REPO_ROOT / "deploy/docker/docker-compose.yml"

MARKER = "# hive (#1696)"
CARRIER = "X-Hive-Upstream-Auth"

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok   {message}")
    else:
        print(f"  FAIL {message}")
        failures.append(message)


# --------------------------------------------------------------------------
# The module, loaded and driven for real.
# --------------------------------------------------------------------------


GATEWAY = "http://edge-api:8080/v1"


def load_module(sessions: dict[str, str], resolutions: list[str]) -> types.ModuleType:
    """hive_embed_attribution with Open WebUI's resolver stubbed.

    `sessions` maps a user id to the access token that user's stored OAuth
    session would yield; an id absent from it resolves to nothing, which is the
    fail-closed case. `resolutions` records every id the resolver was asked
    about, so the cache can be observed rather than assumed.
    """
    fake_main = types.ModuleType("open_webui.main")
    fake_main.app = types.SimpleNamespace(state=types.SimpleNamespace(oauth_manager=object()))

    async def get_system_oauth_token(request, user):  # noqa: ANN001
        resolutions.append(user.id)
        token = sessions.get(user.id)
        return {"access_token": token} if token else None

    fake_middleware = types.ModuleType("open_webui.utils.middleware")
    fake_middleware.get_system_oauth_token = get_system_oauth_token

    package = types.ModuleType("open_webui")
    package.__path__ = []
    utils_package = types.ModuleType("open_webui.utils")
    utils_package.__path__ = []

    sys.modules["open_webui"] = package
    sys.modules["open_webui.main"] = fake_main
    sys.modules["open_webui.utils"] = utils_package
    sys.modules["open_webui.utils.middleware"] = fake_middleware

    # The environment is the authority on where a user credential may go, so it
    # has to be set for the module to attach anything at all.
    os.environ["RAG_OPENAI_API_BASE_URL"] = GATEWAY
    os.environ["OPENAI_API_BASE_URL"] = GATEWAY

    spec = importlib.util.spec_from_file_location("hive_embed_attribution", MODULE)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeUser:
    def __init__(self, user_id: str) -> None:
        self.id = user_id


def behaviour() -> None:
    print("\nbehaviour: the carrier is attached, or the call is refused")

    resolutions: list[str] = []
    module = load_module({"user-a": "token-a", "user-b": "token-b"}, resolutions)

    base = {"Content-Type": "application/json", "Authorization": "Bearer hk_shim_key"}

    got = asyncio.run(module.attach(base, FakeUser("user-a"), GATEWAY))
    check(
        got.get(CARRIER) == "Bearer token-a",
        "a signed-in user's own token rides on the carrier header",
    )
    check(
        got.get("Authorization") == "Bearer hk_shim_key",
        "Authorization still carries the shim key, which is what gates the carrier at edge-api",
    )
    check(
        base == {"Content-Type": "application/json", "Authorization": "Bearer hk_shim_key"},
        "the caller's header dict is not mutated",
    )

    other = asyncio.run(module.attach(base, FakeUser("user-b"), GATEWAY))
    check(
        other.get(CARRIER) == "Bearer token-b",
        "a second user gets their own token, never the first user's",
    )

    # The whole point of #1696: no attribution means no call, not a call billed
    # to the platform.
    refused = False
    try:
        asyncio.run(module.attach(base, FakeUser("user-with-no-session"), GATEWAY))
    except module.AttributionUnavailable:
        refused = True
    check(refused, "a user with no resolvable credential is REFUSED, not sent under the shim key")

    refused_none = False
    try:
        asyncio.run(module.attach(base, None, GATEWAY))
    except module.AttributionUnavailable:
        refused_none = True
    check(refused_none, "a call with no user at all is refused")

    # Cache behaviour. The first two lookups above are one each; a repeat of
    # user-a must not add a third, and the failed lookup must not be cached.
    before = list(resolutions)
    asyncio.run(module.attach(base, FakeUser("user-a"), GATEWAY))
    check(
        resolutions == before,
        "a resolved token is reused inside its TTL rather than re-read per batch",
    )
    failed_before = resolutions.count("user-with-no-session")
    try:
        asyncio.run(module.attach(base, FakeUser("user-with-no-session"), GATEWAY))
    except module.AttributionUnavailable:
        pass
    check(
        resolutions.count("user-with-no-session") == failed_before + 1,
        "a failed resolution is not cached, so one transient error is not 30 seconds of refusals",
    )

    module.forget("user-a")
    check(
        asyncio.run(module.attach(base, FakeUser("user-a"), GATEWAY)).get(CARRIER) == "Bearer token-a",
        "forgetting a cached token re-resolves it rather than failing closed forever",
    )


def destination() -> None:
    """The HIGH finding from the independent security review of PR #1712.

    `agenerate_openai_batch_embeddings` takes its url from
    `app.state.config.RAG_OPENAI_API_BASE_URL`, which
    `POST /api/v1/retrieval/embedding/update` lets any instance admin rewrite at
    runtime, and on this shared chat instance every tenant OWNER is an instance
    admin. Attaching a per-user session bearer to a destination with that
    property would turn a leaked shared platform key into a cross-tenant session
    harvest, so the module compares the destination against the ENVIRONMENT and
    refuses anything else.
    """
    print("\nsecurity: a user credential only ever goes to the Hive gateway")

    resolutions: list[str] = []
    module = load_module({"user-a": "token-a"}, resolutions)
    base = {"Authorization": "Bearer hk_shim_key"}

    for name, url in (
        ("an attacker-controlled host", "https://evil.example/v1"),
        ("the gateway's name on another port", "http://edge-api:9999/v1"),
        ("the gateway over a different scheme", "https://edge-api:8080/v1"),
        ("an empty destination", ""),
        ("a relative path", "/v1"),
    ):
        refused = False
        try:
            asyncio.run(module.attach(base, FakeUser("user-a"), url))
        except module.AttributionUnavailable:
            refused = True
        check(refused, f"refuses {name}")

    check(
        not resolutions,
        "and refuses BEFORE resolving a credential, so a hostile destination "
        "cannot mint a token or fill the cache",
    )

    got = asyncio.run(module.attach(base, FakeUser("user-a"), GATEWAY))
    check(got.get(CARRIER) == "Bearer token-a", "and still attaches for the real gateway")
    check(
        asyncio.run(module.attach(base, FakeUser("user-a"), "http://edge-api:8080/v1/")).get(CARRIER)
        == "Bearer token-a",
        "matching on origin, so a trailing slash or a different path is not a refusal",
    )

    # Asserted on the AST of the function rather than on the file's text: the
    # module's own comments name app.state.config, because explaining the threat
    # requires naming it, and a substring check over prose cannot tell an
    # explanation from a read.
    origins_fn = None
    for node in ast.walk(ast.parse(MODULE.read_text(encoding="utf-8"))):
        if isinstance(node, ast.FunctionDef) and node.name == "_gateway_origins":
            origins_fn = node
    check(origins_fn is not None, "the allowed origins come from a dedicated function")
    if origins_fn is not None:
        body = ast.dump(ast.Module(body=origins_fn.body, type_ignores=[]))
        check(
            "'environ'" in body,
            "the allowed origins are read from the environment",
        )
        check(
            "'state'" not in body and "'config'" not in body,
            "and never from the admin-writable persistent config",
        )


def threaded_producers() -> None:
    """The MEDIUM finding from the same review: two producers call the embedding
    function with no user at all, so the new refusal would break them, and the
    first fails inside a swallowed except where nobody would connect it to this
    change."""
    print("\nthe two producers that had no user now carry one")

    knowledge = patched_source(VENDORED_KNOWLEDGE, "HIVE_OWUI_KNOWLEDGE_PY")
    check(
        "EMBEDDING_FUNCTION(content, user=user)" in knowledge,
        "embed_knowledge_base_metadata embeds for a user rather than for nobody",
    )
    check(
        knowledge.count("user=user") >= 6,
        "and all six call sites hand one over (create, admin reindex, both "
        "external source paths, update, and file add)",
    )
    tree = ast.parse(knowledge)
    for node in ast.walk(tree):
        if isinstance(node, ast.AsyncFunctionDef) and node.name == "embed_knowledge_base_metadata":
            names = [a.arg for a in node.args.args]
            check("user" in names, "the signature takes a user")
            check(
                node.args.defaults and isinstance(node.args.defaults[-1], ast.Constant),
                "with a default, so a caller added upstream still type checks; it "
                "fails at the gateway rather than billing the platform",
            )
            break
    else:
        check(False, "embed_knowledge_base_metadata still exists")

    builtin = patched_source(VENDORED_BUILTIN, "HIVE_OWUI_BUILTIN_PY")
    check(
        "EMBEDDING_FUNCTION(query, user=__user__)" in builtin,
        "the knowledge-base search builtin embeds for the caller",
    )
    check(
        "Users.get_user_by_id" in MODULE.read_text(encoding="utf-8"),
        "and a mapping principal is resolved through Open WebUI's own model "
        "layer rather than wrapped in a stand-in that assumes what the resolver reads",
    )


# --------------------------------------------------------------------------
# The splice, applied to the vendored copies of the files the image runs.
# --------------------------------------------------------------------------


def patched_source(which: Path = None, env_var: str = "HIVE_OWUI_RETRIEVAL_UTILS_PY") -> str:
    """One of the three patched files, as the image will run it.

    All three are patched in one run rather than one at a time, because the
    patch script asserts every anchor before it writes anything: applying it to
    a single file with the other two pointed at copies is the only way to get
    the same all-or-nothing behaviour the image build gets.
    """
    which = which or VENDORED_UTILS
    with tempfile.TemporaryDirectory() as tmp:
        env = dict(os.environ)
        for var, source in (
            ("HIVE_OWUI_RETRIEVAL_UTILS_PY", VENDORED_UTILS),
            ("HIVE_OWUI_KNOWLEDGE_PY", VENDORED_KNOWLEDGE),
            ("HIVE_OWUI_BUILTIN_PY", VENDORED_BUILTIN),
        ):
            target = Path(tmp) / f"{var}.py"
            target.write_text(source.read_text(encoding="utf-8"), encoding="utf-8")
            env[var] = str(target)
        result = subprocess.run(
            [sys.executable, str(PATCH_SCRIPT)], env=env, capture_output=True, text=True
        )
        if result.returncode != 0:
            raise AssertionError(
                "the patch did not apply to the vendored open-webui sources:\n"
                + result.stdout
                + result.stderr
            )
        return Path(env[env_var]).read_text(encoding="utf-8")


def splice() -> None:
    print("\nsplice: applied at the chokepoint, before the upstream request")

    source = patched_source()
    check(source.count(MARKER) == 1, "the marker is inserted exactly once")

    tree = ast.parse(source)
    target = None
    for node in ast.walk(tree):
        if isinstance(node, ast.AsyncFunctionDef) and node.name == "agenerate_openai_batch_embeddings":
            target = node
    check(target is not None, "agenerate_openai_batch_embeddings still exists")
    if target is None:
        return

    attach_lines = [
        node.lineno
        for node in ast.walk(target)
        if isinstance(node, ast.Await)
        and isinstance(node.value, ast.Call)
        and isinstance(node.value.func, ast.Attribute)
        and node.value.func.attr == "attach"
    ]
    post_lines = [
        node.lineno
        for node in ast.walk(target)
        if isinstance(node, ast.Call)
        and isinstance(node.func, ast.Attribute)
        and node.func.attr == "post"
    ]
    check(len(attach_lines) == 1, "the attribution call is inside the embedding function, exactly once")
    check(bool(post_lines), "the upstream POST is still in that function")
    if attach_lines and post_lines:
        check(
            attach_lines[0] < min(post_lines),
            "attribution runs BEFORE the request, so it cannot decorate a call already sent",
        )

    check(
        "hive_embed_attribution.attach(headers, user, url)" in source,
        "the splice hands the destination over, so the gateway check has something "
        "to verify against",
    )
    check(
        "embeddings = await agenerate_openai_batch_embeddings(" in source,
        "generate_embeddings still dispatches to the function this patch covers",
    )
    # The sync sibling is deliberately unpatched, so state that it is still
    # unreachable rather than leaving a reader to wonder whether it was missed.
    body = re.sub(r"^def generate_openai_batch_embeddings\(", "", source, flags=re.MULTILINE)
    callers = re.findall(r"[^a]generate_openai_batch_embeddings\(", body)
    check(
        not callers,
        "the sync generate_openai_batch_embeddings still has no caller, so leaving it unpatched covers nothing",
    )


# --------------------------------------------------------------------------
# The two halves have to ship together.
# --------------------------------------------------------------------------


def wiring() -> None:
    print("\nwiring: the Go half, the compose wiring and the image build")

    go = UNWRAP_GO.read_text(encoding="utf-8")
    check(
        'path == "/v1/embeddings"' in go,
        "edge-api requires a per-user token on /v1/embeddings, so a missing carrier refuses "
        "instead of billing the shim",
    )
    check(
        f'UpstreamAuthHeader = "{CARRIER}"' in go,
        "the header name this module writes is the one edge-api reads",
    )
    check(
        CARRIER in MODULE.read_text(encoding="utf-8"),
        "the module names the same carrier header",
    )

    compose = COMPOSE.read_text(encoding="utf-8")
    check(
        "RAG_OPENAI_API_KEY: ${OWUI_SHIM_KEY:-}" in compose,
        "the chat container still authenticates its embeddings with the shim key, which is why "
        "the carrier is needed at all",
    )

    dockerfile = DOCKERFILE.read_text(encoding="utf-8")
    check(
        "apply_embed_attribution_1696_patch.py" in dockerfile,
        "the image build applies the patch",
    )
    check(
        "owui-patches/hive_embed_attribution.py /app/backend/open_webui/utils/hive_embed_attribution.py"
        in dockerfile,
        "the image build installs the module the splice imports",
    )
    check(
        f"grep -c '{MARKER}'" in dockerfile,
        "the image build pins the marker count, so a silent no-op fails the build",
    )

    vendored, pinned = vendored_and_pinned_versions()
    check(
        vendored is not None and vendored == pinned,
        f"the vendored tree ({vendored}) and the pinned image ({pinned}) are the same version, "
        "so patching the vendored copy describes the source the image runs",
    )


def main() -> int:
    behaviour()
    destination()
    threaded_producers()
    splice()
    wiring()
    print()
    if failures:
        print(f"FAILED: {len(failures)} check(s)")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("all checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
