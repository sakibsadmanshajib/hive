#!/usr/bin/env python3
"""Pin the Caddyfile.supabase route split against silent reopening.

Several properties in that file are load bearing, and every one of them fails
silently: the config stays valid, Caddy starts, every legitimate request still
works, and the only difference is that something which should be refused is
not. The cutover rests on them.

  1. Nothing on the public port reaches the internal route set. While the two
     site blocks shared port 80, a request carrying "Host: caddy-supabase"
     against the public port received /rest/v1, /storage/v1 and the admin API,
     because a client-supplied Host header was the only thing selecting
     between them.
  2. The @admin refusal precedes the proxy INSIDE the public snippet. handle
     directives are evaluated in written order, so moving that one block below
     the proxy takes /auth/v1/admin/users and /auth/v1/invite from 404 to
     proxied, with nothing else observable changing.
  3. The public snippet exposes /auth/v1 alone. PostgREST connects as a
     superuser and a token's role claim selects a database role, so a publicly
     reachable /rest/v1 is the whole schema behind whatever grants happen to
     exist. Storage is the same argument for objects.
  4. The rate-limit bucket key cannot be chosen by the caller: X-Forwarded-For
     is rewritten to the peer address, and Sb-Forwarded-For, which GoTrue reads
     FIRST, is stripped.

The checks are structural rather than name based, which matters more than it
sounds. An earlier version of this file looked for the string "@admin" and for
a site block spelled "caddy-supabase". Both passed while the protection was
gone: "@admin" finds the matcher DEFINITION, which Caddy treats as
order-independent, and a new site block on :8080 under any other name imports
whatever it likes. So this parses the file into snippets and site blocks and
asks about ports and imports.

Same spirit as test_caddy_owui_blocklist.py: the file is parsed rather than
duplicated, so editing the Caddyfile is what this measures. No framework, no
Docker, no network. Run via `make test-scripts`.
"""

import pathlib
import re
import sys

HERE = pathlib.Path(__file__).resolve().parent.parent
CADDYFILE = HERE / "deploy" / "docker" / "Caddyfile.supabase"
COMPOSE = HERE / "deploy" / "docker" / "docker-compose.enterprise.yml"

PUBLIC_SNIPPET = "supabase_public"
INTERNAL_SNIPPET = "supabase_internal"
CORS_SNIPPET = "supabase_auth_cors_preflight"
PUBLIC_PORT = "8080"

# All four are required for a browser to accept the preflight, and the echo
# placeholder is what keeps Allow-Headers from being a list that goes stale.
CORS_RESPONSE_HEADERS = [
    "Access-Control-Allow-Origin",
    "Access-Control-Allow-Methods",
    "Access-Control-Allow-Headers",
    "Access-Control-Max-Age",
]
CORS_ECHO_PLACEHOLDER = "{header.Access-Control-Request-Headers}"

# Upstream guards /invite with requireAdminCredentials while leaving it outside
# the /admin group, so it needs naming separately. Re-read GoTrue's route list
# on every image bump: this tracks someone else's invariant. At v2.189.0
# requireAdminCredentials appears exactly twice, on /invite and on the group.
REQUIRED_ADMIN_PATHS = ["/auth/v1/admin", "/auth/v1/admin/*", "/auth/v1/invite"]

# Never reachable from the public listener without the RLS policies and grants
# that would make it safe, and there are none.
INTERNAL_ONLY_PREFIXES = ["/rest/v1", "/storage/v1"]

# The bucket key GoTrue is told to use, and the value it must carry.
RATE_LIMIT_HEADER = "X-Forwarded-For"
RATE_LIMIT_VALUE = "{remote_host}"
# Read before the configured header by performRateLimiting, so it has to go.
STRIPPED_HEADER = "Sb-Forwarded-For"

failures = []


def fail(msg):
    failures.append(msg)


def strip_comments(text):
    """Drop whole-line comments so a commented-out directive cannot satisfy a
    check, and a comment mentioning a prefix cannot trip one."""
    return "\n".join(line for line in text.splitlines() if not line.strip().startswith("#"))


# Caddy placeholders are brace delimited too ({$SUPABASE_DOMAIN:...},
# {remote_host}), and counting them as block braces cuts a site header in half.
# They never contain whitespace, which is what separates them from a real
# block brace, so they are masked out for the depth walk and restored after.
PLACEHOLDER = re.compile(r"\{([^{}\s]*)\}")


def mask_placeholders(text):
    return PLACEHOLDER.sub(lambda m: "\x01" + m.group(1) + "\x02", text)


def unmask_placeholders(text):
    return text.replace("\x01", "{").replace("\x02", "}")


def parse_blocks(raw):
    """Split a Caddyfile into (header, body) pairs at brace depth zero.

    Header is whatever precedes the opening brace: "(snippet_name)" for a
    snippet, an address list for a site, empty for the global options block.
    """
    text = mask_placeholders(raw)
    blocks = []
    depth = 0
    header_start = 0
    body_start = None
    for i, ch in enumerate(text):
        if ch == "{":
            depth += 1
            if depth == 1:
                header = text[header_start:i].strip()
                body_start = i + 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                blocks.append((unmask_placeholders(header), unmask_placeholders(text[body_start:i])))
                header_start = i + 1
            elif depth < 0:
                raise AssertionError("unbalanced closing brace in the Caddyfile")
    if depth != 0:
        raise AssertionError("unbalanced opening brace in the Caddyfile")
    return blocks


def ports_of(address):
    """The TCP port a single site address binds.

    ":8080" is a port-only address (any host). Otherwise an explicit :port
    suffix wins, and failing that the scheme decides: https is 443, anything
    else is 80. Placeholders such as {$SUPABASE_DOMAIN:supabase.localhost} may
    contain colons, so the port is read from the tail after the last brace.
    """
    addr = address.strip().rstrip(",")
    if not addr:
        return None
    tail = addr[addr.rindex("}") + 1:] if "}" in addr else addr
    m = re.search(r":(\d+)$", tail)
    if m:
        return m.group(1)
    return "443" if addr.startswith("https://") else "80"


def snippet_body(blocks, name):
    for header, body in blocks:
        if header == "(" + name + ")":
            return body
    raise AssertionError("snippet " + name + " is missing from the Caddyfile")


def imports(body):
    return set(re.findall(r"^\s*import\s+(\S+)", body, re.M))


def check_public_snippet(public):
    # The admin refusal has to come first, or it never runs. Search for the
    # handle DIRECTIVE, not the @admin matcher definition: Caddy orders
    # directives, and treats a named matcher's definition as position
    # independent, so anchoring on the definition passes while the protection
    # is gone.
    admin_at = public.find("handle @admin")
    proxy_at = public.find("handle_path /auth/v1/*")
    if admin_at == -1:
        fail("the public snippet has no `handle @admin` block; the admin API is reachable from outside")
    if proxy_at == -1:
        fail("the public snippet no longer proxies /auth/v1")
    if admin_at != -1 and proxy_at != -1 and admin_at > proxy_at:
        fail(
            "`handle @admin` now follows `handle_path /auth/v1/*`, so it never matches: "
            "handle directives are evaluated in written order and the proxy takes the "
            "request first (/auth/v1/admin/* and /auth/v1/invite become proxied)"
        )

    matcher = re.search(r"@admin\s+path\s+([^\n]+)", public)
    if not matcher:
        fail("could not read the @admin path matcher out of the public snippet")
    else:
        declared = matcher.group(1).split()
        for want in REQUIRED_ADMIN_PATHS:
            if want not in declared:
                fail("the @admin matcher no longer covers " + want)

    for prefix in INTERNAL_ONLY_PREFIXES:
        if prefix in public:
            fail(
                "the public snippet mentions " + prefix + ": that backend must not be "
                "reachable from a browser without the RLS policies and grants to make it safe"
            )

    # Pin the VALUE, not just the header name: rewriting it from another
    # request header hands the rate-limit bucket straight back to the caller.
    if (RATE_LIMIT_HEADER + " " + RATE_LIMIT_VALUE) not in public:
        fail(
            "the public proxy no longer rewrites " + RATE_LIMIT_HEADER + " to "
            + RATE_LIMIT_VALUE + ", so the GoTrue rate-limit bucket may be caller-chosen"
        )
    if ("-" + STRIPPED_HEADER) not in public:
        fail(
            "the public proxy no longer strips " + STRIPPED_HEADER + ", which GoTrue reads "
            "BEFORE the configured header, so enabling one GoTrue flag would let a caller "
            "pick its own rate-limit bucket"
        )


def check_cors_preflight(blocks, public, internal):
    """The /auth/v1 CORS preflight is terminated by the gateway, and its headers
    stay inside the preflight-only handle.

    Both halves fail silently and in opposite directions. Remove the block and
    GoTrue answers the browser's preflight with no Access-Control-* headers at
    all, because `apikey` is not on its fixed allow-list, so every supabase-js
    call fails with "Failed to fetch" before it is sent. Hoist the headers out
    of the handle to snippet level and they also land on the PROXIED response
    beside the Access-Control-Allow-Origin GoTrue sets itself, and a browser
    rejects a duplicated value exactly as hard as a missing one. A probe that
    only sends OPTIONS cannot see the second case.
    """
    try:
        body = snippet_body(blocks, CORS_SNIPPET)
    except AssertionError:
        fail(
            "snippet " + CORS_SNIPPET + " is gone: GoTrue answers the browser's own preflight "
            "with no Access-Control-* headers, because `apikey`, which supabase-js sends on "
            "every request, is not on its fixed allow-list"
        )
        return

    if "method OPTIONS" not in body:
        fail(CORS_SNIPPET + " no longer matches on OPTIONS, so it can short-circuit a real request")
    if "path /auth/v1/*" not in body:
        fail(CORS_SNIPPET + " no longer scopes itself to /auth/v1")

    handle_at = body.find("handle @auth_preflight")
    if handle_at == -1:
        fail(CORS_SNIPPET + " has no preflight-only handle; its headers would reach real responses")
    for name in CORS_RESPONSE_HEADERS:
        at = body.find(name)
        if at == -1:
            fail(CORS_SNIPPET + " no longer sets " + name + ", and a browser needs all four")
        elif handle_at != -1 and at < handle_at:
            fail(
                name + " is set outside `handle @auth_preflight`, so it also lands on the "
                "proxied response next to GoTrue's own; a duplicated "
                "Access-Control-Allow-Origin blocks the browser as hard as a missing one"
            )

    # Echoed, not enumerated. A fixed list is what broke this in the first
    # place, and the next header supabase-js adds would break it identically.
    if CORS_ECHO_PLACEHOLDER not in body:
        fail(
            "Access-Control-Allow-Headers is no longer echoed from "
            + CORS_ECHO_PLACEHOLDER + "; a fixed list breaks the moment supabase-js sends one "
            "more header, with the same symptom that names nothing"
        )

    for label, snippet in (("public", public), ("internal", internal)):
        if CORS_SNIPPET not in imports(snippet):
            fail("the " + label + " snippet no longer imports " + CORS_SNIPPET)
        if "Access-Control-Allow-Origin" in snippet:
            fail(
                "the " + label + " snippet sets Access-Control-Allow-Origin itself; the one "
                "place that may is the preflight-only handle in " + CORS_SNIPPET
            )

    # Order inside the public snippet: after the admin refusal, so an OPTIONS to
    # /auth/v1/admin is refused with the rest of the admin API rather than told
    # it would be welcome, and before the proxy, or GoTrue answers it instead.
    admin_at = public.find("handle @admin")
    import_at = public.find("import " + CORS_SNIPPET)
    proxy_at = public.find("handle_path /auth/v1/*")
    if -1 not in (admin_at, import_at) and import_at < admin_at:
        fail(
            "the public snippet imports " + CORS_SNIPPET + " before `handle @admin`, so a "
            "preflight to /auth/v1/admin is answered 204 instead of refused with the rest "
            "of the admin API"
        )
    if -1 not in (import_at, proxy_at) and import_at > proxy_at:
        fail(
            "the public snippet imports " + CORS_SNIPPET + " after the /auth/v1 proxy, so the "
            "preflight reaches GoTrue and is refused there"
        )


def check_sites(blocks):
    """Nothing bound to the public port may reach the internal route set.

    Structural on purpose: a new site block on :8080 under any name at all is
    caught, because the question asked is about the port and the imports, not
    about the hostname anyone happened to write.
    """
    public_sites = []
    for header, body in blocks:
        if not header or header.startswith("("):
            continue
        addresses = [a for a in re.split(r"[,\s]+", header) if a]
        on_public = any(ports_of(a) == PUBLIC_PORT for a in addresses)
        used = imports(body)
        if on_public:
            public_sites.append(header)
            if INTERNAL_SNIPPET in used:
                fail(
                    "site `" + header + "` is bound to the public port and imports "
                    + INTERNAL_SNIPPET + ", which puts /rest/v1, /storage/v1 and the admin "
                    "API back on the public listener"
                )
            # Every proxy definition on this port has to live in the public
            # snippet, which is the thing under review. A site that imports it
            # and then adds its own handle_path would pass an "imports the
            # right snippet" check while publishing whatever it likes.
            if "reverse_proxy" in body:
                fail(
                    "site `" + header + "` is bound to the public port and proxies directly; "
                    "proxy definitions belong in the " + PUBLIC_SNIPPET + " snippet, where the "
                    "route set is reviewed, not in a site block beside it"
                )
        elif PUBLIC_SNIPPET in used:
            fail("site `" + header + "` imports " + PUBLIC_SNIPPET + " off the public port")

    if not public_sites:
        fail(
            "no site is bound to port " + PUBLIC_PORT + ": the public route set has to live on "
            "its own listener, or the split depends on the client sending an honest Host header"
        )

    domain_site = [h for h in public_sites if "SUPABASE_DOMAIN" in h]
    if not domain_site:
        fail("the SUPABASE_DOMAIN site is not bound to port " + PUBLIC_PORT)

    catch_all = [h for h in public_sites if h.strip() == ":" + PUBLIC_PORT]
    if not catch_all:
        fail(
            "no catch-all on :" + PUBLIC_PORT + "; an unmatched Host there gets Caddy's "
            "empty 200 instead of a refusal"
        )


def check_rate_limit_agreement():
    """GoTrue keys its limits on the header named in compose, and the Caddyfile
    rewrites a header by name. Two settings that must agree, in two files, with
    nothing else noticing when they stop."""
    compose = strip_comments(COMPOSE.read_text())
    m = re.search(r"GOTRUE_RATE_LIMIT_HEADER:\s*\$\{ENTERPRISE_RATE_LIMIT_HEADER:-([^}]+)\}", compose)
    if not m:
        fail("could not read the GOTRUE_RATE_LIMIT_HEADER default out of the enterprise compose file")
        return
    configured = m.group(1).strip()
    if configured != RATE_LIMIT_HEADER:
        fail(
            "GoTrue is told to key its rate limits on " + configured + " while the gateway "
            "rewrites " + RATE_LIMIT_HEADER + ", so the header GoTrue reads is whatever the "
            "caller sent (ENTERPRISE_RATE_LIMIT_HEADER can do this silently at runtime too)"
        )


def main():
    text = strip_comments(CADDYFILE.read_text())
    blocks = parse_blocks(text)

    public = snippet_body(blocks, PUBLIC_SNIPPET)
    internal = snippet_body(blocks, INTERNAL_SNIPPET)

    check_public_snippet(public)
    check_cors_preflight(blocks, public, internal)
    for prefix in INTERNAL_ONLY_PREFIXES:
        if prefix not in internal:
            fail("the internal snippet no longer routes " + prefix + ", which in-stack callers need")
    check_sites(blocks)
    check_rate_limit_agreement()

    if failures:
        print("Caddyfile.supabase route split: FAIL")
        for f in failures:
            print("  - " + f)
        return 1
    print("Caddyfile.supabase route split: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
