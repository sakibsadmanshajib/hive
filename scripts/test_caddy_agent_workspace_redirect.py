#!/usr/bin/env python3
"""Pin the retirement of the /agent-workspace route on the public chat origin.

D-045 ruling 1: the agent surface is a mode of the chat composer, not a
destination. It has no route, no navigation entry and no sign-in of its own,
because a run IS a conversation and there is nothing separate to authenticate
into. The route this file guards violated that directly: `/agent-workspace/*`
proxied to `apps/agent-console`, a second whole application with its own
Supabase session, whose unauthenticated response is a page headed "Sign in to
run agent tasks". That is the second sign-in gate the owner rejected on
2026-08-23, reachable by anyone holding an old bookmark or a typed URL.

The route is redirected rather than deleted. Three live callers still point at
it and a 404 would break all three for no gain: the desktop app derives its
console URL from `/agent-workspace` (apps/desktop/src/settings.ts), the chat
pipeline action emits `/agent-workspace/tasks` as a link
(deploy/docker/pipelines/hive_agent_console_action.py), and browsers hold
bookmarks from the months the surface shipped. A redirect lands every one of
them on the chat root, which is where Cowork now lives, so the old URL keeps
working and the gate behind it becomes unreachable.

Two directions of drift are both failures:

  * The reverse proxy comes back, by revert or by merge, and the second
    sign-in gate is public again.
  * The redirect narrows and stops covering a path the proxy used to serve,
    so an old bookmark starts 404ing inside the chat SPA instead of landing on
    the composer.

The matcher is read out of the Caddyfile and evaluated here rather than being
described in prose, so editing the Caddyfile is what this test measures.

No framework, no network, no Docker. Run via `make test-scripts`.
"""

import pathlib
import re
import sys

CADDYFILE = (
    pathlib.Path(__file__).resolve().parent.parent
    / "deploy"
    / "docker"
    / "Caddyfile.owui"
)

# Every request path the retired reverse proxy used to serve. Each must now
# answer with a redirect to the chat root instead of reaching agent-console.
MUST_REDIRECT = [
    # The bare entry URL, which is what a typed or bookmarked link looks like
    # and what apps/desktop/src/settings.ts builds.
    "/agent-workspace",
    "/agent-workspace/",
    # The task list, the page that rendered the rejected sign-in gate.
    "/agent-workspace/tasks",
    # The gate itself, and the OAuth callback beside it.
    "/agent-workspace/auth/sign-in",
    "/agent-workspace/auth/callback",
    # Next.js build assets and API routes under the same prefix. They are
    # dead weight once the pages are gone, but they were proxied, so they are
    # covered here too rather than left to a different outcome.
    "/agent-workspace/_next/static/chunks/main.js",
    "/agent-workspace/api/deck/abc",
]

# Paths that must keep their own behaviour. A redirect that swallowed any of
# these would take out a live surface.
MUST_NOT_REDIRECT = [
    # The chat root itself, which is the redirect target.
    "/",
    # The in-shell agent route (#944): unlinked, still reachable by URL so runs
    # submitted before the composer mode existed can still be opened.
    "/agents",
    "/agents/",
    # The agent-task API on this origin, same-origin so the browser needs no
    # CORS preflight (Caddyfile.owui @agentApi).
    "/v1/agent/tasks",
    # Hive's own FastAPI router inside the chat container.
    "/api/v1/hive/agent/tasks",
    # A prefix that merely starts with the retired one. Subtree, not substring.
    "/agent-workspaces",
    "/agent-workspace-archive/tasks",
]


def _strip_comments(text: str) -> str:
    return "\n".join(
        line for line in text.splitlines() if not line.lstrip().startswith("#")
    )


def _matcher_patterns(directives: str, matcher: str) -> list[str]:
    """The path patterns of a named `path` matcher, in written order."""
    found = re.search(
        rf"^\s*{re.escape(matcher)}\s+path\s+(.+)$", directives, re.MULTILINE
    )
    if not found:
        return []
    return found.group(1).split()


def _glob_to_regex(pattern: str) -> re.Pattern[str]:
    """Caddy's `path` matcher: case-insensitive, `*` spans any run of characters.

    Only `*` is expanded; everything else is literal. Go's path matcher has no
    other metacharacter, so nothing else needs escaping away.
    """
    body = "".join(".*" if part == "*" else re.escape(part) for part in re.split(r"(\*)", pattern))
    return re.compile(rf"^{body}$", re.IGNORECASE)


def main() -> int:
    text = CADDYFILE.read_text(encoding="utf-8")
    directives = _strip_comments(text)
    failures: list[str] = []

    # 1. The proxy is gone. Checked against the upstream address rather than
    # against the old matcher name, because renaming the matcher while keeping
    # the proxy is the one edit that would satisfy a name check and still ship
    # the gate.
    for line in directives.splitlines():
        if "reverse_proxy" in line and "agent-console" in line:
            failures.append(
                "Caddyfile.owui proxies the public chat origin to agent-console "
                f"again ({line.strip()}), which republishes the separate "
                '"Sign in to run agent tasks" gate D-045 rejected'
            )

    # 2. The redirect exists, targets the chat root, and is permanent.
    redirect = re.search(
        r"^\s*redir\s+(@\w+)\s+(\S+)(?:\s+(\S+))?\s*$", directives, re.MULTILINE
    )
    if not redirect:
        failures.append(
            "Caddyfile.owui has no `redir` for the retired agent workspace, so "
            "an old bookmark now 404s inside the chat SPA instead of landing on "
            "the composer that replaced the surface"
        )
        patterns: list[str] = []
    else:
        matcher, target, status = redirect.group(1), redirect.group(2), redirect.group(3)
        if target != "/":
            failures.append(
                f"the agent workspace redirect targets {target!r}, not the chat "
                "root; Cowork is a composer mode on `/` (D-045 ruling 1) and "
                "there is no other surface to send these URLs to"
            )
        if status != "308":
            failures.append(
                f"the agent workspace redirect uses status {status!r}; 308 keeps "
                "the method and is cacheable, which is what a retired route "
                "wants (Caddy's bare `redir` default is 302)"
            )
        patterns = _matcher_patterns(directives, matcher)
        if not patterns:
            failures.append(
                f"the redirect names matcher {matcher} but no `{matcher} path ...` "
                "line defines it, so it matches nothing"
            )

    # 3. The matcher covers exactly what the proxy covered.
    if patterns:
        compiled = [_glob_to_regex(pattern) for pattern in patterns]

        def redirected(path: str) -> bool:
            return any(rx.match(path) for rx in compiled)

        for path in MUST_REDIRECT:
            if not redirected(path):
                failures.append(
                    f"{path} is no longer redirected, so a URL the proxy used to "
                    f"serve now falls through to Open WebUI (patterns: {patterns})"
                )
        for path in MUST_NOT_REDIRECT:
            if redirected(path):
                failures.append(
                    f"{path} is swallowed by the agent workspace redirect, which "
                    f"takes out a live surface (patterns: {patterns})"
                )

    if failures:
        print(f"Caddyfile.owui agent workspace retirement ({len(failures)} failure(s)):")
        for line in failures:
            print(f"  {line}")
        return 1

    print(
        f"Caddyfile.owui: agent-console unreachable, {len(MUST_REDIRECT)} retired "
        f"paths redirect to the composer, {len(MUST_NOT_REDIRECT)} live paths "
        "untouched, as expected"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
