#!/usr/bin/env python3
"""Pin the public auth origin that Caddyfile.console serves at /auth/v1.

A self-hosted data plane has no browser-reachable Supabase address of its own:
SUPABASE_URL is a compose service name. The console origin therefore carries
/auth/v1 itself, proxied to caddy-supabase's public listener, so supabase-js in
the console bundle and the endpoints inside GoTrue's discovery document both
resolve for a real browser. See the comment block in Caddyfile.console.

Every property below fails silently, which is why they are pinned here rather
than left to a reviewer noticing:

  1. Wrong upstream port. caddy-supabase:80 is the INTERNAL route set, so
     /rest/v1, /storage/v1 and an unrefused admin API arrive on a public
     hostname. supabase-auth:9999 is GoTrue with no route filtering at all.
     Both answer sign-in perfectly, so a login test passes either way and the
     only difference is what else the internet can now reach.
  2. Prefix stripped. `handle_path` strips /auth/v1, and the public listener is
     the thing that strips it, so every route 404s. The config stays valid.
  3. Host disagreement. The public listener is host matched on SUPABASE_DOMAIN
     and answers 404 from its catch-all otherwise, so a Host rewrite that
     drifts from the value caddy-supabase reads breaks every auth request while
     both containers stay healthy.
  4. Matcher widened. `/auth/*` instead of `/auth/v1*` swallows the console's
     own /auth/sign-in page, which is a self-inflicted outage on the surface
     this route exists to make work.
  5. Ordering. The catch-all handle must stay the fallback, or it takes the
     auth request first and Next.js answers 404.

Structural, and mutation tested: `--self-check` asserts the real files pass and
then that each named mutation FAILS. A check nobody has watched fail is not a
check. No framework, no Docker, no network. Run via `make test-scripts`.
"""
from __future__ import annotations

import argparse
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).resolve().parent.parent
CONSOLE = HERE / "deploy" / "docker" / "Caddyfile.console"
SUPABASE = HERE / "deploy" / "docker" / "Caddyfile.supabase"
COMPOSE = HERE / "deploy" / "docker" / "docker-compose.yml"

PUBLIC_PORT = "8080"
# Exactly these two, and nothing broader. The bare form is here so a request to
# /auth/v1 lands on the gateway rather than falling through to a Next.js 404
# that names nothing.
REQUIRED_AUTH_PATHS = ["/auth/v1", "/auth/v1/*"]
# Paths the console app serves itself, which the auth matcher must never take.
CONSOLE_OWN_PATHS = ["/auth/sign-in", "/auth/callback", "/oauth/consent", "/console"]
APP_UPSTREAM = "web-console-prod:3000"
# Never reachable from a browser: no RLS policies or grants exist that would
# make either safe to serve anonymously.
INTERNAL_ONLY_PREFIXES = ["/rest/v1", "/storage/v1"]


def strip_comments(text: str) -> str:
    """Drop whole-line comments and fold backslash continuations.

    Both matter. A commented-out directive must not satisfy a check, and this
    file's own comment block names every upstream and every port these checks
    look for, so reading it unstripped makes most of them vacuously true.
    """
    folded = re.sub(r"\\\n\s*", " ", text)
    return "\n".join(
        line for line in folded.splitlines() if not line.strip().startswith("#")
    )


def block_body(text: str, header_at: int) -> str | None:
    """Body of the brace block whose header starts at `header_at`."""
    open_at = text.find("{", header_at)
    if open_at == -1:
        return None
    depth = 0
    for i in range(open_at, len(text)):
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
            if depth == 0:
                return text[open_at + 1 : i]
    return None


def supabase_public_host_expression(supabase_text: str) -> str | None:
    """The host expression caddy-supabase's public site block is matched on.

    Read out of the file rather than duplicated here, so a change to the
    default on that side is a divergence this check sees instead of a value
    that quietly stops matching.
    """
    for line in strip_comments(supabase_text).splitlines():
        stripped = line.strip()
        if not stripped.endswith("{"):
            continue
        addr = stripped[:-1].strip()
        if not addr.endswith(":" + PUBLIC_PORT):
            continue
        m = re.search(r"\{\$SUPABASE_DOMAIN[^}]*\}", addr)
        if m:
            return m.group(0)
    return None


def compose_caddy_console_env(compose_text: str) -> str | None:
    """The caddy-console service's environment block, or None."""
    m = re.search(r"^  caddy-console:\s*$", compose_text, re.MULTILINE)
    if not m:
        return None
    body = compose_text[m.end() :]
    # Next top-level service header ends this one.
    nxt = re.search(r"^  [a-z0-9][a-z0-9._-]*:\s*$", body, re.MULTILINE)
    if nxt:
        body = body[: nxt.start()]
    return body


def check(console_raw: str, supabase_raw: str, compose_raw: str) -> list[str]:
    failures: list[str] = []

    def fail(msg: str) -> None:
        failures.append(msg)

    console = strip_comments(console_raw)
    compose = strip_comments(compose_raw)

    # A NAMED matcher, and reading it that way is not a style choice. `handle`
    # takes exactly one matcher token, so `handle /auth/v1 /auth/v1/*` is a
    # parse error Caddy refuses to adapt. The first version of this check
    # accepted that form and reported OK on a Caddyfile that crash-looped the
    # container on the box, which is why CI now adapts every Caddyfile as well:
    # a structural check reads text and cannot know what parses.
    #
    # Anchored on the `handle @auth` DIRECTIVE for the ordering question below,
    # because a named matcher's definition is position independent and
    # anchoring on the definition would pass while the route was dead.
    auth_m = re.search(r"^\s*handle\s+@auth\s*\{", console, re.MULTILINE)
    matcher = re.search(r"^\s*@auth\s+path\s+([^\n]+)$", console, re.MULTILINE)
    if not auth_m or not matcher:
        fail(
            "Caddyfile.console has no `@auth path ...` matcher paired with a `handle @auth` "
            "block, so this origin serves no public auth surface: supabase-js in the console "
            "bundle and every endpoint in GoTrue's discovery document point at a compose "
            "service name a browser cannot resolve, and browser login is down with nothing in "
            "any log naming the cause"
        )
        return failures

    declared = matcher.group(1).split()
    for want in REQUIRED_AUTH_PATHS:
        if want not in declared:
            fail("the auth handle's path matcher no longer covers " + want)
    # The inverse, which a "login works" test cannot see: a matcher that grew
    # until it swallowed a page this app serves itself.
    for own in CONSOLE_OWN_PATHS:
        for pattern in declared:
            prefix = pattern.removesuffix("*")
            if own == prefix or (pattern.endswith("*") and own.startswith(prefix)):
                fail(
                    "the auth handle's matcher `" + pattern + "` also takes " + own + ", which "
                    "the console app serves itself: this routes the app's own pages to GoTrue"
                )

    if re.search(r"^\s*handle_path\s+(@auth|/auth/v1)", console, re.MULTILINE):
        fail(
            "the auth route uses handle_path, which strips /auth/v1 before proxying. The "
            "public listener is what strips that prefix, so GoTrue is handed /token on a "
            "listener expecting /auth/v1/token and every auth route 404s"
        )

    auth_body = block_body(console, auth_m.start())
    if auth_body is None:
        fail("could not read the auth handle's body out of Caddyfile.console")
        return failures

    upstream = re.search(r"reverse_proxy\s+(\S+)", auth_body)
    if not upstream:
        fail("the auth handle proxies nothing")
    elif upstream.group(1) != "caddy-supabase:" + PUBLIC_PORT:
        fail(
            "the auth handle proxies to " + upstream.group(1) + " rather than caddy-supabase:"
            + PUBLIC_PORT + ". Port 80 on that container is the INTERNAL route set (/rest/v1, "
            "/storage/v1 and an unrefused admin API) and supabase-auth:9999 is GoTrue with no "
            "route filtering at all, so either one publishes all of it on this public hostname "
            "while sign-in keeps working exactly as before"
        )

    want_host = supabase_public_host_expression(supabase_raw)
    if want_host is None:
        fail(
            "no site block bound to :" + PUBLIC_PORT + " in Caddyfile.supabase carries a "
            "{$SUPABASE_DOMAIN...} host expression, so there is nothing for this origin's Host "
            "rewrite to agree with"
        )
    else:
        host_up = re.search(r"header_up\s+Host\s+(\S+)", auth_body)
        if not host_up:
            fail(
                "the auth handle does not rewrite Host. caddy-supabase's public site is host "
                "matched, and an unmatched Host on that port hits its catch-all, so every auth "
                "request answers 404 while both containers report healthy"
            )
        elif host_up.group(1) != want_host:
            fail(
                "the auth handle sends Host " + host_up.group(1) + " while caddy-supabase's "
                "public site matches on " + want_host + ". The two must be the same expression, "
                "or an operator setting SUPABASE_DOMAIN moves one side and not the other and "
                "every auth request 404s"
            )

    # The catch-all must stay the app's, and must stay last. Caddy sorts handle
    # blocks by matcher specificity and a matcher-less handle is the least
    # specific, so it is the fallback under that rule; asserting source order
    # too means the file is correct under either reading rather than depending
    # on which one is true.
    catchall = re.search(r"^\s*handle\s*\{", console, re.MULTILINE)
    if not catchall:
        fail(
            "Caddyfile.console has no matcher-less `handle {` block, so nothing serves the "
            "console app itself"
        )
    else:
        if catchall.start() < auth_m.start():
            fail(
                "the matcher-less `handle {` fallback now precedes the auth handle. It matches "
                "every request, so the auth route is dead and Next.js answers /auth/v1 with a "
                "404 that names nothing"
            )
        body = block_body(console, catchall.start())
        if body is None or APP_UPSTREAM not in body:
            fail(
                "the matcher-less handle no longer proxies " + APP_UPSTREAM + ", so this origin "
                "serves the auth API and not the console app"
            )

    for prefix in INTERNAL_ONLY_PREFIXES:
        if prefix in console:
            fail(
                "Caddyfile.console routes " + prefix + ", which must not be reachable from a "
                "browser without the RLS policies and grants to make it safe, and there are none"
            )

    # Both containers have to be handed the variable, or the two defaults are
    # what agree and setting SUPABASE_DOMAIN in .env moves only caddy-supabase.
    env = compose_caddy_console_env(compose)
    if env is None:
        fail("could not find the caddy-console service in docker-compose.yml")
    elif want_host is not None:
        # `{$SUPABASE_DOMAIN:supabase.localhost}` -> `supabase.localhost`
        inner = want_host[2:-1]
        _, _, default = inner.partition(":")
        m = re.search(r"^\s*SUPABASE_DOMAIN:\s*(\S+)\s*$", env, re.MULTILINE)
        if not m:
            fail(
                "the caddy-console service does not pass SUPABASE_DOMAIN, so its Caddyfile falls "
                "back to the compiled default while caddy-supabase uses whatever .env says, and "
                "setting that variable takes browser login down"
            )
        elif m.group(1) != "${SUPABASE_DOMAIN:-" + default + "}":
            fail(
                "caddy-console passes SUPABASE_DOMAIN as " + m.group(1) + ", which does not "
                "match the default caddy-supabase's own site expression carries (" + default
                + "): an unset variable then makes the two sides disagree"
            )

    return failures


# Each mutation is one way this has gone wrong or could, keyed by which file it
# edits. Written as replacements on the REAL text, so a mutation that no longer
# applies is itself a failure rather than a silently skipped case.
MUTATIONS: dict[str, tuple[str, str, str]] = {
    "the auth route deleted outright": (
        "console",
        "  handle @auth {",
        "  handle @nothing {",
    ),
    "the matcher definition deleted, leaving the handle pointing at nothing": (
        "console",
        "  @auth path /auth/v1 /auth/v1/*\n",
        "",
    ),
    "the prefix stripped before proxying": (
        "console",
        "  handle @auth {",
        "  handle_path @auth {",
    ),
    "proxied to the internal listener instead of the public one": (
        "console",
        "reverse_proxy caddy-supabase:8080 {",
        "reverse_proxy caddy-supabase:80 {",
    ),
    "proxied straight to GoTrue, past every route refusal": (
        "console",
        "reverse_proxy caddy-supabase:8080 {",
        "reverse_proxy supabase-auth:9999 {",
    ),
    "the Host rewrite dropped": (
        "console",
        "      header_up Host {$SUPABASE_DOMAIN:supabase.localhost}\n",
        "",
    ),
    "the Host rewrite pinned to a literal that cannot follow the variable": (
        "console",
        "header_up Host {$SUPABASE_DOMAIN:supabase.localhost}",
        "header_up Host supabase.localhost",
    ),
    "the matcher widened until it takes the app's own sign-in page": (
        "console",
        "  @auth path /auth/v1 /auth/v1/*",
        "  @auth path /auth/v1 /auth/*",
    ),
    "the matcher narrowed to the bare form, dropping every real route": (
        "console",
        "  @auth path /auth/v1 /auth/v1/*",
        "  @auth path /auth/v1",
    ),
    "the app fallback hoisted above the auth route": (
        "console",
        "  handle @auth {",
        "  handle {\n    respond 204\n  }\n\n  handle @auth {",
    ),
    "the app fallback no longer serving the app": (
        "console",
        "    reverse_proxy web-console-prod:3000 {",
        "    reverse_proxy caddy-supabase:8080 {",
    ),
    "PostgREST published on the browser-facing origin": (
        "console",
        "  handle @auth {",
        "  handle /rest/v1/* {\n    reverse_proxy supabase-rest:3000\n  }\n\n  handle @auth {",
    ),
    "caddy-console no longer handed the domain variable": (
        "compose",
        "      SUPABASE_DOMAIN: ${SUPABASE_DOMAIN:-supabase.localhost}\n",
        "",
    ),
    "the two default domains drifted apart": (
        "compose",
        "      SUPABASE_DOMAIN: ${SUPABASE_DOMAIN:-supabase.localhost}",
        "      SUPABASE_DOMAIN: ${SUPABASE_DOMAIN:-supabase.internal}",
    ),
    "the gateway's own public host expression changed without this side following": (
        "supabase",
        "http://{$SUPABASE_DOMAIN:supabase.localhost}:8080 {",
        "http://{$SUPABASE_PUBLIC_DOMAIN:supabase.localhost}:8080 {",
    ),
}


def self_check() -> int:
    real = {
        "console": CONSOLE.read_text(),
        "supabase": SUPABASE.read_text(),
        "compose": COMPOSE.read_text(),
    }
    failures = check(real["console"], real["supabase"], real["compose"])
    assert not failures, failures

    for label, (target, old, new) in MUTATIONS.items():
        texts = dict(real)
        assert old in texts[target], f"{label}: the text it mutates is gone: {old!r}"
        texts[target] = texts[target].replace(old, new, 1)
        got = check(texts["console"], texts["supabase"], texts["compose"])
        assert got, f"{label} did not fail"

    print(f"test_caddy_console_auth_origin self-check: OK ({len(MUTATIONS)} mutations)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-check", action="store_true")
    args = parser.parse_args()
    if args.self_check:
        return self_check()

    failures = check(CONSOLE.read_text(), SUPABASE.read_text(), COMPOSE.read_text())
    if failures:
        print("Caddyfile.console public auth origin: FAIL")
        for f in failures:
            print("  - " + f)
        return 1
    print("Caddyfile.console public auth origin: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
