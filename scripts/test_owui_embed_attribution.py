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

    got = asyncio.run(module.attach(base, FakeUser("user-a")))
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

    other = asyncio.run(module.attach(base, FakeUser("user-b")))
    check(
        other.get(CARRIER) == "Bearer token-b",
        "a second user gets their own token, never the first user's",
    )

    # The whole point of #1696: no attribution means no call, not a call billed
    # to the platform.
    refused = False
    try:
        asyncio.run(module.attach(base, FakeUser("user-with-no-session")))
    except module.AttributionUnavailable:
        refused = True
    check(refused, "a user with no resolvable credential is REFUSED, not sent under the shim key")

    refused_none = False
    try:
        asyncio.run(module.attach(base, None))
    except module.AttributionUnavailable:
        refused_none = True
    check(refused_none, "a call with no user at all is refused")

    # Cache behaviour. The first two lookups above are one each; a repeat of
    # user-a must not add a third, and the failed lookup must not be cached.
    before = list(resolutions)
    asyncio.run(module.attach(base, FakeUser("user-a")))
    check(
        resolutions == before,
        "a resolved token is reused inside its TTL rather than re-read per batch",
    )
    failed_before = resolutions.count("user-with-no-session")
    try:
        asyncio.run(module.attach(base, FakeUser("user-with-no-session")))
    except module.AttributionUnavailable:
        pass
    check(
        resolutions.count("user-with-no-session") == failed_before + 1,
        "a failed resolution is not cached, so one transient error is not 30 seconds of refusals",
    )

    module.forget("user-a")
    check(
        asyncio.run(module.attach(base, FakeUser("user-a"))).get(CARRIER) == "Bearer token-a",
        "forgetting a cached token re-resolves it rather than failing closed forever",
    )


# --------------------------------------------------------------------------
# The splice, applied to the vendored copy of the file the image runs.
# --------------------------------------------------------------------------


def patched_source() -> str:
    with tempfile.TemporaryDirectory() as tmp:
        target = Path(tmp) / "utils.py"
        target.write_text(VENDORED_UTILS.read_text(encoding="utf-8"), encoding="utf-8")
        env = dict(os.environ, HIVE_OWUI_RETRIEVAL_UTILS_PY=str(target))
        result = subprocess.run(
            [sys.executable, str(PATCH_SCRIPT)], env=env, capture_output=True, text=True
        )
        if result.returncode != 0:
            raise AssertionError(
                "the patch did not apply to the vendored retrieval/utils.py:\n"
                + result.stdout
                + result.stderr
            )
        return target.read_text(encoding="utf-8")


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
