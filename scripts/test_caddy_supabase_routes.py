#!/usr/bin/env python3
"""Pin the Caddyfile.supabase route split against silent reopening.

Three properties in this file are load bearing, and all three fail silently:
the config stays valid, Caddy starts, every legitimate request still works,
and the only difference is that something which should be refused is not.

  1. The public site listens on its OWN port. While the public and internal
     site blocks shared port 80, a request carrying "Host: caddy-supabase"
     against the public port received the full internal route set: /rest/v1,
     /storage/v1 and the admin API. Host matching is client supplied and case
     insensitive, so it enforced nothing. A separate port does.
  2. The @admin refusal precedes the proxy inside the public snippet. handle
     blocks are mutually exclusive and evaluated in written order, so swapping
     those two blocks moves /auth/v1/admin/users from 404 to a proxied request
     with nothing else observable changing, and no other test in this
     repository fails.
  3. The public snippet exposes /auth/v1 alone. PostgREST connects to Postgres
     as a superuser and a token's role claim selects a database role, so a
     publicly reachable /rest/v1 is the whole schema behind whatever grants
     happen to exist. Storage is the same argument for objects.

Same shape as test_caddy_owui_blocklist.py: the file is parsed rather than
duplicated, so editing the Caddyfile is what this measures. No framework, no
Docker, no network. Run via `make test-scripts`.
"""

import pathlib
import re
import sys

CADDYFILE = pathlib.Path(__file__).resolve().parent.parent / "deploy" / "docker" / "Caddyfile.supabase"

PUBLIC_SNIPPET = "supabase_public"
INTERNAL_SNIPPET = "supabase_internal"

# Upstream guards /invite with requireAdminCredentials while leaving it outside
# the /admin group, so it needs naming separately. Re-read GoTrue's route list
# on every image bump: this list is tracking someone else's invariant.
REQUIRED_ADMIN_PATHS = ["/auth/v1/admin", "/auth/v1/admin/*", "/auth/v1/invite"]

# Never reachable from the public listener without RLS policies and grants
# that do not exist yet.
FORBIDDEN_PUBLIC_PREFIXES = ["/rest/v1", "/storage/v1"]

failures = []


def fail(msg):
    failures.append(msg)


def snippet_body(text, name):
    """Return the body of a Caddy snippet definition, e.g. (name) { ... }."""
    start = text.index("(" + name + ") {")
    depth = 0
    for i in range(start, len(text)):
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
            if depth == 0:
                return text[start:i + 1]
    raise AssertionError("unbalanced braces in snippet " + name)


def main():
    text = CADDYFILE.read_text()

    public = snippet_body(text, PUBLIC_SNIPPET)
    internal = snippet_body(text, INTERNAL_SNIPPET)

    # 1. The admin refusal has to come first, or it never runs.
    admin_at = public.find("@admin")
    proxy_at = public.find("handle_path /auth/v1/*")
    if admin_at == -1:
        fail("the public snippet has no @admin matcher; the admin API is reachable from outside")
    if proxy_at == -1:
        fail("the public snippet no longer proxies /auth/v1")
    if admin_at != -1 and proxy_at != -1 and admin_at > proxy_at:
        fail(
            "the @admin handle now follows handle_path /auth/v1/*, so it never matches: "
            "handle blocks are evaluated in written order and the proxy takes the request first"
        )

    # 2. Every admin-credentialed route upstream keeps outside /admin.
    matcher = re.search(r"@admin\s+path\s+([^\n]+)", public)
    if not matcher:
        fail("could not read the @admin path matcher out of the public snippet")
    else:
        declared = matcher.group(1).split()
        for want in REQUIRED_ADMIN_PATHS:
            if want not in declared:
                fail("the @admin matcher no longer covers " + want)

    # 3. Nothing but auth on the public listener.
    for prefix in FORBIDDEN_PUBLIC_PREFIXES:
        if prefix in public:
            fail(
                "the public snippet mentions " + prefix + ": that backend must not be reachable "
                "from a browser without the RLS policies and grants to make it safe"
            )
    for prefix in FORBIDDEN_PUBLIC_PREFIXES:
        if prefix not in internal:
            fail("the internal snippet no longer routes " + prefix + ", which in-stack callers need")

    # 4. The public site keeps its own listener port, and the internal sites
    #    keep theirs. Host matching alone is not enforcement.
    public_site = re.search(r"http://\{\$SUPABASE_DOMAIN[^}]*\}(:\d+)?\s*\{", text)
    if not public_site:
        fail("could not find the public site block")
    elif public_site.group(1) != ":8080":
        fail(
            "the public site no longer binds its own port (found "
            + str(public_site.group(1))
            + "): sharing a port with the internal sites makes the route split depend on the "
            "client sending an honest Host header"
        )
    if not re.search(r"^:8080\s*\{", text, re.M):
        fail("no catch-all on :8080; an unmatched Host there gets Caddy's empty 200")
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith(("http://caddy-supabase", "https://caddy-supabase")) and ":8080" in stripped:
            fail("an internal site is bound to the public port 8080: " + stripped)

    # 5. The rate-limit key must not be caller-controlled. GoTrue keys its
    #    limits on this header, and Caddy appends to an inbound value unless
    #    told to replace it.
    if "header_up X-Forwarded-For" not in public:
        fail(
            "the public proxy no longer overwrites X-Forwarded-For, so a caller can vary it and "
            "hand itself a fresh GoTrue rate-limit bucket per request"
        )

    if failures:
        print("Caddyfile.supabase route split: FAIL")
        for f in failures:
            print("  - " + f)
        return 1
    print("Caddyfile.supabase route split: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
