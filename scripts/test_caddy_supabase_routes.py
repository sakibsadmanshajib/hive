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
# Every entry needs its trailing-slash form as well. Caddy's `path` matcher is
# EXACT, while GoTrue's router registers both `pattern` and `pattern/`, so
# /auth/v1/invite/ matched nothing and was proxied through. /auth/v1/admin/*
# covered the admin group only by accident of its wildcard.
#
# /oauth/clients/register is dynamic OAuth client registration, live because
# this deployment sets GOTRUE_OAUTH_SERVER_ENABLED=true. Public, it would let
# anyone mint a client for this issuer.
REQUIRED_ADMIN_PATHS = [
    "/auth/v1/admin",
    "/auth/v1/admin/*",
    "/auth/v1/invite",
    "/auth/v1/invite/*",
    "/auth/v1/oauth/clients/register",
    "/auth/v1/oauth/clients/register/*",
]

# Self-service account creation, refused on the public listener so that the
# posture survives someone editing GOTRUE_DISABLE_SIGNUP. /otp and /magiclink
# are here because they create a user too: should_create_user defaults to true,
# so they are signup routes wearing a login name.
REQUIRED_SELF_SERVICE_BLOCKED_PATHS = [
    "/auth/v1/signup",
    "/auth/v1/signup/*",
    "/auth/v1/magiclink",
    "/auth/v1/magiclink/*",
]

# Every login and recovery route the product actually uses. Blocking one of
# these would be a self-inflicted outage, so the deny list is checked against
# them: a matcher that swallowed /token would pass a "signup is blocked" test
# while locking every user out.
REQUIRED_PUBLIC_AUTH_PATHS = [
    # /otp is on this list, not on the blocked list, and the reason is a live
    # caller: apps/web-console/components/email-settings-card.tsx sends
    # signInWithOtp to re-send an email verification, passing
    # shouldCreateUser false so it cannot create anything. The first draft of
    # the deny list blocked it, which would have been an outage on a working
    # feature to close a hole GOTRUE_DISABLE_SIGNUP already closes.
    "/auth/v1/otp",
    "/auth/v1/token",
    "/auth/v1/authorize",
    "/auth/v1/callback",
    "/auth/v1/verify",
    "/auth/v1/recover",
    "/auth/v1/user",
    "/auth/v1/logout",
]

# Never reachable from the public listener without the RLS policies and grants
# that would make it safe, and there are none.
INTERNAL_ONLY_PREFIXES = ["/rest/v1", "/storage/v1"]

# The bucket key GoTrue is told to use, and the value it must carry.
#
# {client_ip}, not {remote_host}, since issue #1744. The peer of this listener
# is always caddy-console, so keying on it gave the whole internet one bucket:
# thirty requests from one host exhausted the deployment's hourly quota and
# 429'd every other user's password reset. {client_ip} resolves to the first
# untrusted hop of X-Forwarded-For, which is the address Cloudflare reported,
# and it only does that because of TRUSTED_PROXIES below.
RATE_LIMIT_HEADER = "X-Forwarded-For"
RATE_LIMIT_VALUE = "{client_ip}"
# Without a trusted set Caddy treats every peer as untrusted and {client_ip}
# collapses back to the peer address, silently restoring the single bucket.
TRUSTED_PROXIES = "trusted_proxies static private_ranges"
# Go duration syntax, which is what time.ParseDuration accepts: one or more
# number+unit pairs, no spaces, no bare numbers.
GO_DURATION = re.compile(r"(\d+(\.\d+)?(ns|us|\u00b5s|ms|s|m|h))+")
# Read before the configured header by performRateLimiting, so it has to go.
STRIPPED_HEADER = "Sb-Forwarded-For"

failures = []


def fail(msg):
    failures.append(msg)


def strip_comments(text):
    """Drop whole-line comments so a commented-out directive cannot satisfy a
    check, and a comment mentioning a prefix cannot trip one.

    Backslash continuations are folded in the same pass, and that is load
    bearing rather than tidy. Caddy lets a directive span lines with a trailing
    backslash, and every matcher check below reads its argument list with a
    pattern that stops at the newline. A path list broken across two lines
    therefore looked like a SHORTER list, and the checks reported the entries on
    the later lines as missing, which is the benign direction. The dangerous
    direction is the same shape: a list that grew onto a second line would have
    had its new entries invisible to the over-block assertions. Folding first
    means the formatting of the file cannot change what these checks see.
    """
    folded = re.sub(r"\\\n\s*", " ", text)
    return "\n".join(line for line in folded.splitlines() if not line.strip().startswith("#"))


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


def nested_block(body, header_prefix):
    """Body of the first block inside `body` whose header starts with the prefix.

    parse_blocks only walks depth zero, and a check that a directive sits inside
    a particular block cannot be answered by comparing string offsets: anything
    after that block's CLOSING brace compares as "after" it too. So this returns
    the block's own text, and the caller asks about that.
    """
    text = mask_placeholders(body)
    at = text.find(header_prefix)
    if at == -1:
        return None
    open_at = text.find("{", at)
    if open_at == -1:
        return None
    depth = 0
    for i in range(open_at, len(text)):
        if text[i] == "{":
            depth += 1
        elif text[i] == "}":
            depth -= 1
            if depth == 0:
                return unmask_placeholders(text[open_at + 1:i])
    return None


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

    # Same ordering trap as @admin, same reasoning: anchor on the handle
    # directive, because a named matcher's definition is position independent
    # and would pass while the protection was dead.
    selfserve_at = public.find("handle @selfserve")
    if selfserve_at == -1:
        fail(
            "the public snippet has no `handle @selfserve` block, so self-service "
            "account creation is reachable from outside and the only thing stopping "
            "it is GOTRUE_DISABLE_SIGNUP, one environment variable away from open"
        )
    elif proxy_at != -1 and selfserve_at > proxy_at:
        fail(
            "`handle @selfserve` now follows `handle_path /auth/v1/*`, so it never "
            "matches and /auth/v1/signup is proxied again"
        )

    selfserve = re.search(r"@selfserve\s+path\s+([^\n]+)", public)
    if not selfserve:
        fail("could not read the @selfserve path matcher out of the public snippet")
    else:
        declared = selfserve.group(1).split()
        for want in REQUIRED_SELF_SERVICE_BLOCKED_PATHS:
            if want not in declared:
                fail("the @selfserve matcher no longer covers " + want)
        # The inverse, which is the failure a signup-only test cannot see: a
        # deny list that grew until it took a route the product needs.
        for keep in REQUIRED_PUBLIC_AUTH_PATHS:
            # Both the bare form and its wildcard, since blocking either one
            # takes the route out.
            if keep in declared or (keep + "/*") in declared:
                fail(
                    "the @selfserve matcher now blocks " + keep + ", which is a login "
                    "or recovery route the product uses: this locks users out rather "
                    "than closing signup"
                )

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


def without_response_handlers(text):
    """Drop every `handle_response ... { ... }` body, braces balanced.

    Text inside one describes a response Caddy builds itself, which is a
    different thing from text that decorates a proxied response, and the two
    cannot be told apart by substring."""
    out = []
    i = 0
    while True:
        at = text.find("handle_response", i)
        if at < 0:
            out.append(text[i:])
            return "".join(out)
        brace = text.find("{", at)
        if brace < 0:
            out.append(text[i:])
            return "".join(out)
        out.append(text[i:at])
        depth = 0
        j = brace
        while j < len(text):
            if text[j] == "{":
                depth += 1
            elif text[j] == "}":
                depth -= 1
                if depth == 0:
                    break
            j += 1
        i = j + 1


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

    matcher = nested_block(body, "@auth_preflight")
    if matcher is None:
        fail(CORS_SNIPPET + " has no @auth_preflight matcher block")
    else:
        if "method OPTIONS" not in matcher:
            fail(
                "@auth_preflight no longer matches on OPTIONS alone, so the handle can "
                "short-circuit a real request and answer 204 to a sign-in"
            )
        if "path /auth/v1/*" not in matcher:
            fail("@auth_preflight no longer scopes itself to /auth/v1")

    handled = nested_block(body, "handle @auth_preflight")
    if handled is None:
        fail(CORS_SNIPPET + " has no preflight-only handle; its headers would reach real responses")
    else:
        if "respond 204" not in handled:
            fail(
                "the preflight handle no longer answers 204; a browser treats anything "
                "other than a 2xx as a failed preflight"
            )
        for name in CORS_RESPONSE_HEADERS:
            if name not in handled:
                fail(CORS_SNIPPET + " no longer sets " + name + " on the preflight, and a browser needs all four")

    # Asked of the handle's own text, not of the snippet: a directive that sits
    # after the handle's closing brace is outside it while comparing as "after"
    # it, and it is exactly the placement that puts a second
    # Access-Control-Allow-Origin on the proxied response.
    for name in CORS_RESPONSE_HEADERS:
        if name in body and (handled is None or name not in handled):
            fail(
                name + " is set outside `handle @auth_preflight`, so it also lands on the "
                "proxied response next to GoTrue's own; a duplicated "
                "Access-Control-Allow-Origin blocks the browser as hard as a missing one"
            )

    # Echoed, not enumerated, and bound to the header it answers: the
    # placeholder appearing somewhere else in the snippet must not satisfy this.
    # A fixed list is what broke this in the first place, and the next header
    # supabase-js adds would break it identically.
    echoed = re.compile(
        r"Access-Control-Allow-Headers\s+" + re.escape(CORS_ECHO_PLACEHOLDER)
    )
    if handled is not None and not echoed.search(handled):
        fail(
            "Access-Control-Allow-Headers is no longer echoed from "
            + CORS_ECHO_PLACEHOLDER + "; a fixed list breaks the moment supabase-js sends one "
            "more header, with the same symptom that names nothing"
        )

    for label, snippet in (("public", public), ("internal", internal)):
        if CORS_SNIPPET not in imports(snippet):
            fail("the " + label + " snippet no longer imports " + CORS_SNIPPET)
        # handle_response bodies are excluded, and only those. The harm this
        # rule exists for is a SECOND Access-Control-Allow-Origin landing beside
        # the one GoTrue sets on a proxied response, which a browser rejects as
        # hard as a missing one. A handle_response replaces the upstream
        # response outright, discarding its headers, so there is nothing there
        # to duplicate; the /auth/v1/recover route relies on that to answer a
        # real and an unknown address with one identical header set (#1744).
        # Anywhere else in the snippet the old rule still bites.
        if "Access-Control-Allow-Origin" in without_response_handlers(snippet):
            fail(
                "the " + label + " snippet sets Access-Control-Allow-Origin outside a "
                "handle_response; the places that may are the preflight-only handle in "
                + CORS_SNIPPET + " and a handler that replaces the upstream response"
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


def check_unmatched_host_catch_alls(text):
    """Both listeners must answer 404 to a Host that matches no site block.

    Caddy answers an EMPTY 200 to an unmatched host, and the public port has had
    a `:8080 { respond 404 }` catch-all for that reason from the start. Port 80
    did not, and while an empty 200 reaches no backend and is therefore not an
    exposure, it is actively misleading: a probe reading 200 concludes the
    request was proxied and the upstream answered it. That happened during this
    file's own verification and the wrong conclusion nearly shipped as evidence.
    """
    for port, label in (("8080", "public"), ("80", "in-network")):
        block = re.search(
            r"^:" + port + r"\s*\{\s*\n\s*respond 404\s*\n\s*\}", text, re.MULTILINE
        )
        if not block:
            fail(
                "no `:" + port + " { respond 404 }` catch-all for the " + label + " listener: "
                "an unmatched Host gets Caddy's empty 200, which reads as a proxied success"
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


def check_trusted_proxies(text):
    """{client_ip} is only the client's address when Caddy has a trusted set.
    With none configured every peer is untrusted, {client_ip} is the peer, and
    the deployment is back to one rate-limit bucket for the whole internet with
    nothing failing."""
    if TRUSTED_PROXIES not in text:
        fail(
            "the global block no longer sets '" + TRUSTED_PROXIES + "', so {client_ip} "
            "resolves to the caddy-console peer and every caller shares one GoTrue "
            "rate-limit bucket again (issue #1744)"
        )


def check_recover_refusal_is_indistinguishable(public):
    """A 429 on /auth/v1/recover is an account-existence oracle.

    GoTrue answers that route 200 {} for an address it has never seen. Both
    limits that can refuse it -- the per-IP limiter and the per-address
    GOTRUE_SMTP_MAX_FREQUENCY check -- answer 429, and the per-address one can
    only fire for an address that HAS a user row. So an honest status here
    reads the account list two requests at a time. The route rewrites every 429
    back to the endpoint's own 200 {}."""
    if not re.search(r"@recover\s*\{[^}]*\bpath\s+/auth/v1/recover\b", public):
        fail(
            "the public snippet no longer routes /auth/v1/recover on its own, so a "
            "rate-limited reset answers 429 while an unknown address answers 200, which "
            "tells an attacker which addresses hold accounts (issue #1744)"
        )
        return
    # Both outcomes synthesized from one handler, not one proxied and one
    # rewritten. Two paths that agree on status and body still differed in
    # their header set (Vary, Server, the Via hop count), and GoTrue puts
    # X-Sb-Error-Code: over_email_send_rate_limit on the 429 and on nothing an
    # unknown address ever receives, so copying upstream headers through is not
    # an option either.
    # POST-only, or the exact path outranks the preflight handle's /auth/v1/*
    # (handle blocks sort by path specificity, not source order) and the
    # browser's OPTIONS goes to GoTrue, which answers it with no
    # Access-Control-* headers and so blocks the POST that follows.
    if not re.search(r"@recover\s*\{[^}]*\bmethod\s+POST\b", public):
        fail(
            "the /auth/v1/recover matcher no longer restricts itself to POST, so it also "
            "takes the CORS preflight away from the handle that answers it and a browser "
            "cannot send the request at all (issue #1744)"
        )
    # 5xx is the cheapest of the three oracles: GoTrue reaches its mail send
    # only for an address that has a user row, so a broken relay answers a real
    # address 500 and an unknown one 200, in one request, with no window to arm.
    if "@sameanswer status 200 429 5xx" not in public:
        fail(
            "the /auth/v1/recover route no longer answers 200, 429 and 5xx from ONE "
            "handler. Two paths differ in their headers even when status and body match, and "
            "dropping 5xx hands back the one-request oracle a stopped mail relay opens "
            "(issue #1744)"
        )
    if 'respond "{}" 200' not in public:
        fail(
            "the /auth/v1/recover route no longer answers the endpoint's own 200 {}, so a "
            "refusal distinguishes a real address from an unknown one (issue #1744)"
        )
    if "copy_response_headers" in public:
        fail(
            "the public snippet copies upstream response headers; on /auth/v1/recover that "
            "carries GoTrue's X-Sb-Error-Code straight to the caller, which names the refusal "
            "and so names the account (issue #1744)"
        )


def check_per_address_cap():
    """The four GOTRUE_RATE_LIMIT_* values are keyed on the caller's address, so
    a botnet multiplies every one of them. GOTRUE_SMTP_MAX_FREQUENCY is keyed on
    the recipient's user row and is the only cap that survives that."""
    compose = strip_comments(COMPOSE.read_text())
    m = re.search(
        r"GOTRUE_SMTP_MAX_FREQUENCY:\s*\$\{ENTERPRISE_SMTP_MAX_FREQUENCY:-([^}]*)\}", compose
    )
    if not m:
        fail(
            "GOTRUE_SMTP_MAX_FREQUENCY is gone from the enterprise compose file, so the "
            "only per-RECIPIENT cap on auth mail falls back to GoTrue's 1m default and "
            "one address can be mailed 60 times an hour from enough source addresses "
            "(issue #1744)"
        )
        return
    # The runtime harness pins its own value, so it proves GoTrue honours a
    # duration, never that this file supplies a usable one. GoTrue parses this
    # with time.ParseDuration and then treats a zero as unset, falling back to
    # its 1m default, so a bare number or a 0 ships the default while every
    # other check here stays green.
    value = m.group(1).strip()
    if not GO_DURATION.fullmatch(value):
        fail(
            "GOTRUE_SMTP_MAX_FREQUENCY defaults to " + repr(value) + ", which is not a Go "
            "duration. GoTrue parses this with time.ParseDuration, so a bare number is not "
            "a shorter window, it is a boot failure (issue #1744)"
        )
    elif re.fullmatch(r"0+(\.0+)?[a-z\u00b5]+", value):
        fail(
            "GOTRUE_SMTP_MAX_FREQUENCY defaults to " + repr(value) + ", and GoTrue treats a "
            "zero duration as unset and substitutes its own 1m default, so the per-recipient "
            "cap silently reverts (issue #1744)"
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
    check_trusted_proxies(text)
    check_recover_refusal_is_indistinguishable(public)
    check_per_address_cap()

    check_unmatched_host_catch_alls(text)
    if failures:
        print("Caddyfile.supabase route split: FAIL")
        for f in failures:
            print("  - " + f)
        return 1
    print("Caddyfile.supabase route split: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
