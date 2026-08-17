#!/usr/bin/env python3
"""Pin the Caddyfile.owui @blocked matcher against silent widening or narrowing.

The matcher is defence-in-depth on the PUBLIC chat origin. Both directions of
drift are real failures with real history:

  * Too narrow and a credential-bearing admin endpoint reaches Open WebUI.
    That is #769 (`GET /api/v1/configs/export` and `GET /openai/config` return
    the live gateway key, and the first also returns `oauth.client_secret`) and
    the `/api/v1/` half of the signup and admin block, which was anchored at
    `^/+` and so only ever covered the bare SPA routes.
  * Too broad and the chat UI breaks, which is worse than the exposure it
    closes: the exposure needs an authenticated admin session, an outage needs
    nobody. `GET /api/v1/configs/banners` is read on every page load, and
    `/openai/models` plus `/openai/chat/completions` carry the model picker
    and the chat itself.

The OPEN list below is not invented. It is the set of request paths an actually
signed-in Open WebUI session issued against the pinned
`hive-open-webui:v0.10.2-branded` image (sign in, load a chat, open the model
picker, upload a document, open Settings, open Workspace), read back out of a
Caddy access log, plus the endpoints other parts of this repo depend on reaching
through this origin (`POST /api/v1/functions/create`, which is how
hive_jwt_forward installs, and its `/api/v1/functions/id/<id>/...` siblings --
see #736).

`GET /api/v1/terminals/` was on that open list until #770. It is now blocked,
and it is the one path this test moved from one list to the other. That is safe
because its only caller returns `[]` on any non-2xx rather than surfacing an
error, and the message input's terminal menu renders only when that list is
non-empty. Re-captured against the patched image to confirm.

The regex is read out of the Caddyfile rather than duplicated here, so editing
the Caddyfile is what this test measures. Go's regexp and Python's re agree on
every construct used in it (no backreferences, no lookaround).

No framework, no network, no Docker. Run via `make test-scripts`.
"""

import pathlib
import re
import sys

CADDYFILE = pathlib.Path(__file__).resolve().parent.parent / "deploy" / "docker" / "Caddyfile.owui"

# Must be refused by the proxy before Open WebUI ever sees them.
MUST_BLOCK = [
    # #769: credential-bearing admin config reads and writes.
    "/api/v1/configs/export",
    "/openai/config",
    "/openai/config/update",
    # The /api/v1/ mount of the signup and admin family, which the
    # `^/+`-anchored alternatives never covered.
    "/api/v1/auths/signup",
    "/api/v1/auths/admin/details",
    "/api/v1/auths/admin/config",
    "/api/v1/auths/admin/config/ldap",
    "/api/v1/auths/admin/config/ldap/server",
    "/api/v1/auths/admin/config/oauth",
    "/api/v2/auths/signup",
    # #770: the whole terminals router. Every route on it, including the
    # any-verb proxy and the interactive websocket, is gated on
    # `get_verified_user` and not on `get_admin_user`, and each forwards to a
    # registered destination with the credential that destination was
    # registered with. Sub-paths matter as much as the mount: the proxy is
    # /{server_id}/{path} and the websocket is
    # /{server_id}/api/terminals/{session_id}.
    "/api/v1/terminals",
    "/api/v1/terminals/",
    "/api/v1/terminals/srv1/files/list",
    "/api/v1/terminals/srv1/api/terminals/sess1",
    # 2026-08-17: the notes router. Notes is a removed product surface whose
    # backend, unlike the other flag-backed removals, enforces nothing:
    # notes.py checks `notes.enable` on none of its 9 routes, so with the
    # navigation entry and the page route already gone the API stayed callable
    # by any signed-in user. Never on the captured open list, because every
    # front-end caller renders only under `$config.features.enable_notes`.
    "/api/v1/notes",
    "/api/v1/notes/",
    "/api/v1/notes/01234567-89ab-cdef-0123-456789abcdef",
    "/api/v1/notes/01234567-89ab-cdef-0123-456789abcdef/update",
    # #771: the registration endpoints the "Manage Tool Servers" and "Open
    # Terminal" forms write to, plus the children that dial a caller-supplied
    # URL from inside the network.
    "/api/v1/configs/tool_servers",
    "/api/v1/configs/tool_servers/verify",
    "/api/v1/configs/terminal_servers",
    "/api/v1/configs/terminal_servers/verify",
    "/api/v1/configs/terminal_servers/policy",
    "/api/v1/configs/terminal_servers/lifecycle",
    "/api/v1/configs/terminal_servers/refresh",
    # Bare SPA and legacy routes, already covered before this change. Kept so a
    # rewrite of the regex cannot drop them on the way past.
    "/admin",
    "/admin/",
    "/admin/users",
    "//admin",
    "/ADMIN",
    "/signup",
    "/signin",
    "/register",
    "/invite",
    "/create-account",
    "/users/new",
    "/auth/signup",
    "/auths/signup",
]

# Must reach Open WebUI. Every entry without a comment came off the access log
# of a real signed-in session.
MUST_STAY_OPEN = [
    "/",
    "/auth",
    "/health",
    "/manifest.json",
    "/workspace",
    "/ws/socket.io/",
    "/api/changelog",
    "/api/config",
    "/api/models",
    "/api/usage",
    "/api/version",
    "/api/version/updates",
    "/api/v1/auths/",                 # session check, every page load
    "/api/v1/auths/signin",           # the login POST itself
    "/api/v1/auths/update/timezone",
    "/api/v1/configs/banners",        # closest near miss to `configs/export`
    "/api/v1/groups/",
    "/api/v1/models/list",
    "/api/v1/models/tags",
    "/api/v1/models/model/profile/image",
    "/api/v1/tools/",              # Workspace tools, a different router to terminals
    "/api/v1/users/user/settings",
    "/api/v1/users/user/settings/update",
    "/api/v1/users/6fcba712-5631-4e7f-9c9c-ce41047914fa/profile/image",
    "/ollama/api/version",
    "/static/custom.css",
    "/_app/immutable/entry/start.js",
    # Not in that capture, but load-bearing elsewhere and adjacent enough to the
    # blocked segments that a prefix-shaped edit would swallow them.
    "/openai/models",                 # model picker under BYPASS_MODEL_ACCESS_CONTROL
    "/openai/chat/completions",       # the chat itself
    "/api/v1/configs/models",
    "/api/v1/configs/connections",
    # #736 leaves the Function install on this origin until it is rerouted.
    # Blocking these here would break every fresh deploy and CI run.
    "/api/v1/functions/create",
    "/api/v1/functions/id/hive_jwt_forward/update",
    "/api/v1/functions/id/hive_jwt_forward/toggle",
    "/api/v1/functions/id/hive_jwt_forward/toggle/global",
]


def load_blocked_regex(text: str) -> str:
    """Pull the @blocked path_regexp out of the Caddyfile.

    Fails loudly rather than silently testing nothing if the matcher is
    renamed or restructured, which is the failure mode that lets a guard
    quietly stop guarding.
    """
    matches = re.findall(r"^\s*path_regexp\s+blocked\s+(\S+)\s*$", text, re.MULTILINE)
    if len(matches) != 1:
        raise AssertionError(
            f"expected exactly one `path_regexp blocked <re>` line in {CADDYFILE}, "
            f"found {len(matches)}. If the matcher was renamed or split, update this "
            "test deliberately -- do not let it pass by matching nothing."
        )
    return matches[0]


def _dockerfile_runs_patch(dockerfile: pathlib.Path, script: str) -> tuple[bool, bool]:
    """Report whether a Dockerfile both stages and executes an owui patch script.

    Returns `(staged, invoked)`. `staged` is a COPY of the script into the
    image; `invoked` is a RUN that actually executes it with python3. The two
    are checked separately because they fail differently and only the second
    one decides whether the patch reaches the shipped bundle.

    Comments are dropped and backslash continuations are folded into one
    logical line, so a multi-line `RUN a \\\n && b` chain is read as the single
    instruction Docker sees rather than as a sequence of unrelated lines.
    """
    logical: list[str] = []
    buffer = ""
    for raw in dockerfile.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.endswith("\\"):
            buffer += line[:-1] + " "
            continue
        logical.append(buffer + line)
        buffer = ""
    if buffer:
        logical.append(buffer)

    staged = any(
        line.split(maxsplit=1)[0].upper() == "COPY" and script in line for line in logical
    )
    invoked = any(
        line.split(maxsplit=1)[0].upper() == "RUN"
        and re.search(rf"python3?\s+\S*{re.escape(script)}(\s|$)", line)
        for line in logical
    )
    return staged, invoked


def main() -> int:
    text = CADDYFILE.read_text(encoding="utf-8")
    pattern = re.compile(load_blocked_regex(text))

    failures = []
    for path in MUST_BLOCK:
        if not pattern.search(path):
            failures.append(f"NOT BLOCKED but must be: {path}")
    for path in MUST_STAY_OPEN:
        if pattern.search(path):
            failures.append(f"BLOCKED but must stay open: {path}")

    # Guard the two dead globs removed from @adminMutation. Open WebUI mounts no
    # /api/v1/admin and no /api/v1/roles router, so re-adding them would restore
    # coverage that reads real and matches nothing. Comment lines are stripped
    # first: the Caddyfile names both globs in prose to explain the removal.
    directives = "\n".join(
        line for line in text.splitlines() if not line.lstrip().startswith("#")
    )
    for dead in ("/api/v*/admin*", "/api/v*/roles*"):
        if dead in directives:
            failures.append(
                f"dead @adminMutation glob is back: {dead} (no such Open WebUI router)"
            )

    # #770 and #771 are only half closed by the block above. The other half is
    # the image patch that removes Settings > Integrations, and the two must not
    # drift apart in either direction: block without patch leaves a form whose
    # save now 404s, patch without block leaves the terminals proxy reachable.
    #
    # Assert the INVOCATION, not the filename. A substring check for
    # "remove_integrations_tab.py" is satisfied by the COPY line alone, so it
    # stays green with the RUN line neutered, which is the one arrangement that
    # actually ships the panel back. Instructions are joined across backslash
    # continuations first, because the RUN is a multi-line `&&` chain.
    dockerfile = CADDYFILE.parent / "Dockerfile.open-webui"
    staged, invoked = _dockerfile_runs_patch(dockerfile, "remove_integrations_tab.py")
    if not staged:
        failures.append(
            f"{dockerfile.name} no longer COPYs remove_integrations_tab.py into the "
            "image, so the Settings > Integrations forms are back while their "
            "endpoints stay blocked"
        )
    if not invoked:
        failures.append(
            f"{dockerfile.name} no longer RUNs remove_integrations_tab.py, so the "
            "Settings > Integrations forms are back while their endpoints stay blocked"
        )

    if failures:
        print(f"Caddyfile.owui @blocked regression ({len(failures)} failure(s)):")
        for line in failures:
            print(f"  {line}")
        return 1

    print(
        f"Caddyfile.owui @blocked: {len(MUST_BLOCK)} blocked, "
        f"{len(MUST_STAY_OPEN)} open, as expected"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
