#!/usr/bin/env python3
"""Assert the SUPABASE_* configuration a caller is about to use actually works.

This is a preflight, not a test suite. It runs in seconds, writes nothing, and
answers exactly one question: does the Supabase configuration in this process's
environment name a project that is alive, with keys that belong to it?

Why it exists (issue #1059). Six workflows in this repository read `SUPABASE_*`
values from GitHub secrets that name a hosted project which no longer exists.
Nothing failed loudly when that project was deleted. The Cowork proof job went
on reporting for four days over a Supabase that was not there, and
`deploy-web-console-workers` shipped green on the same day the project went
away, because neither of them ever asserts that the configuration resolves
before using it. A green run over a dead backend is worse than a red one: it is
a check that has quietly stopped checking.

The two failures this catches, and nothing else:

  1. The project is gone, unreachable, or the URL is wrong. A deleted hosted
     project keeps resolving in DNS (the wildcard is Supabase's, not the
     project's) and answers HTTP, so a plain connectivity probe passes. Asking
     GoTrue for its health is what separates "a server answered" from "our
     project answered".

  2. The keys name a different project than the URL. This is the shape #1059
     actually took: a URL updated in one place and a key left behind in
     another, or the reverse. A Supabase-issued anon or service-role JWT
     carries a `ref` claim naming its project, and a hosted URL carries the
     same ref as its first hostname label, so the two can be compared offline
     with no request at all. That comparison is free, deterministic, and fails
     on a mismatch that every live probe in the world would let through, since
     both halves are individually valid.

Self-hosted deployments are handled honestly rather than skipped. There the
anon key is a locally signed JWT with no `ref` claim and the URL is a private
hostname, so the cross-check has nothing to compare and says so instead of
inventing a pass. The liveness check still applies and is still the useful half.

Usage:

    set -a; . .env; set +a
    python3 scripts/preflight-supabase-config.py

Required env: SUPABASE_URL, SUPABASE_ANON_KEY.
Optional env: SUPABASE_SERVICE_ROLE_KEY (claim-checked when present, never
sent anywhere), SUPABASE_PREFLIGHT_TIMEOUT (seconds, default 15).

Exits 0 when every check passes, 1 otherwise. It never prints a key, a token,
or any part of one; a key is reported by its length and its non-secret claims.
"""
import base64
import binascii
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

TIMEOUT = float(os.environ.get("SUPABASE_PREFLIGHT_TIMEOUT", "15"))

# Every request this script sends carries an explicit User-Agent, and that is
# load bearing rather than politeness. Both demo hostnames sit behind
# Cloudflare, whose bot rules answer 403 "Error 1010: Access denied ... based on
# your browser's signature" to the literal default urllib sends
# (`Python-urllib/3.x`). Measured, not guessed: the same GET of
# https://chat-hive.scubed.co/api/config returns 403 with that default and 200
# with the string below, from the same host in the same second. Without this this
# preflight would report a live project dead whenever its auth origin is
# published through Cloudflare, which is the shape of false negative that gets a
# useful check deleted.
USER_AGENT = "hive-preflight-supabase-config/1 (+https://github.com/sakibsadmanshajib/hive)"

failures: list[str] = []
notes: list[str] = []


def fail(message: str) -> None:
    print(f"  [FAIL] {message}")
    failures.append(message)


def ok(message: str) -> None:
    print(f"  [PASS] {message}")


def skip(message: str) -> None:
    print(f"  [SKIP] {message}")
    notes.append(message)


def jwt_claims(token: str) -> dict | None:
    """Decode a JWT payload without verifying it.

    Verification is not the point and is not possible here: the signing key is
    the thing we would need the project for. What is wanted is the project the
    key claims to belong to, which is unauthenticated metadata by design.
    Returns None for anything that is not a three-part JWT, which is a legal
    state now that Supabase also issues opaque `sb_publishable_*` keys.
    """
    parts = token.split(".")
    if len(parts) != 3:
        return None
    payload = parts[1]
    try:
        raw = base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4))
        claims = json.loads(raw)
    except (binascii.Error, ValueError, UnicodeDecodeError):
        return None
    return claims if isinstance(claims, dict) else None


def hosted_ref(host: str) -> str | None:
    """The project ref a hosted Supabase URL names, or None if not hosted."""
    if not host.endswith(".supabase.co") and not host.endswith(".supabase.in"):
        return None
    label = host.split(".")[0]
    return label or None


def check_key(name: str, token: str, want_role: str, url_ref: str | None) -> None:
    """Claim-check one key against the URL. Offline; sends nothing."""
    print(f"\n== {name} ==")
    print(f"  present, {len(token)} characters")
    claims = jwt_claims(token)
    if claims is None:
        # A `sb_publishable_*` / `sb_secret_*` key carries no readable claims,
        # so there is nothing to cross-check. Say so rather than pass silently.
        skip(f"{name} is not a JWT, so it carries no project ref to compare")
        return

    role = claims.get("role", "")
    if role == want_role:
        ok(f"{name} carries role={want_role}")
    else:
        fail(f"{name} carries role={role!r}, expected {want_role!r}")

    exp = claims.get("exp")
    if isinstance(exp, (int, float)):
        remaining = exp - time.time()
        if remaining <= 0:
            fail(f"{name} expired {int(-remaining / 86400)} days ago")
        else:
            ok(f"{name} is unexpired, {int(remaining / 86400)} days remaining")

    key_ref = claims.get("ref") or ""
    if url_ref is None:
        skip(f"SUPABASE_URL is not a hosted project URL, so {name}'s ref is not comparable")
    elif not key_ref:
        fail(f"SUPABASE_URL names hosted project {url_ref!r} but {name} carries no ref claim")
    elif key_ref == url_ref:
        ok(f"{name} ref matches SUPABASE_URL project {url_ref!r}")
    else:
        # The #1059 failure, exactly. Both halves are individually valid and
        # every liveness probe passes; only the comparison catches it.
        fail(
            f"{name} belongs to project {key_ref!r} but SUPABASE_URL names {url_ref!r}. "
            "One of the two is stale."
        )


def http_get(url: str, headers: dict) -> tuple[int, str]:
    sent = dict(headers)
    sent.setdefault("User-Agent", USER_AGENT)
    req = urllib.request.Request(url, method="GET", headers=sent)
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
            return r.status, r.read(4096).decode(errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read(4096).decode(errors="replace")
    except Exception as e:  # noqa: BLE001 - a transport failure is a result too
        return 0, f"<transport error: {type(e).__name__}: {e}>"


def main() -> int:
    raw_url = os.environ.get("SUPABASE_URL", "").strip()
    anon = os.environ.get("SUPABASE_ANON_KEY", "").strip()
    service = os.environ.get("SUPABASE_SERVICE_ROLE_KEY", "").strip()

    print("== configuration ==")
    if not raw_url:
        print("  [FAIL] SUPABASE_URL is not set")
        return 1
    if not anon:
        print("  [FAIL] SUPABASE_ANON_KEY is not set")
        return 1

    url = raw_url.rstrip("/")
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme not in ("http", "https") or not parsed.hostname:
        print(f"  [FAIL] SUPABASE_URL is not an absolute http(s) URL: {url!r}")
        return 1

    url_ref = hosted_ref(parsed.hostname)
    kind = f"hosted project {url_ref!r}" if url_ref else "self-hosted or proxied"
    print(f"  [PASS] SUPABASE_URL parses: {parsed.scheme}://{parsed.hostname} ({kind})")

    check_key("SUPABASE_ANON_KEY", anon, "anon", url_ref)
    if service:
        # Claim-checked only. Nothing is ever sent with this key: the admin
        # routes it unlocks are refused at the gateway by design, and a
        # preflight has no business exercising them.
        check_key("SUPABASE_SERVICE_ROLE_KEY", service, "service_role", url_ref)
    else:
        print("\n== SUPABASE_SERVICE_ROLE_KEY ==")
        skip("not set in this environment, nothing to check")

    print("\n== liveness ==")
    # GoTrue's own health endpoint. A deleted hosted project still resolves in
    # DNS and still answers HTTP, so only the project's own service answering
    # is evidence the project exists. Sent with the anon key because the hosted
    # gateway requires one; self-hosted GoTrue ignores the header.
    status, body = http_get(f"{url}/auth/v1/health", {"apikey": anon})
    flat = " ".join(body.split())[:200]
    if status == 200:
        # A 200 is not the evidence. This file's own opening claim is that
        # asking GoTrue for its health separates "a server answered" from "our
        # project answered", and a bare status code does not do that: pointed
        # at https://chat-hive.scubed.co, which is a single-page app that
        # answers 200 with HTML for every path it does not know, the status
        # check passed and this preflight reported the configuration sound.
        # Measured on the live box while writing the post-deploy gate, not
        # imagined. So the body has to be GoTrue's own health document.
        #
        # GoTrue answers {"version": "...", "name": "GoTrue", "description":
        # "..."} on both hosted and self-hosted deployments, so `name` is the
        # thing to insist on, and `version` is accepted as well for a future
        # release that drops the name.
        document = None
        try:
            parsed_body = json.loads(body)
            if isinstance(parsed_body, dict):
                document = parsed_body
        except ValueError:
            document = None
        if document is None:
            fail(
                "GET /auth/v1/health -> 200 with a body that is not JSON, so this URL is "
                f"answering from something other than GoTrue: {flat[:120]}"
            )
        elif str(document.get("name", "")).lower() != "gotrue" and not document.get("version"):
            fail(
                "GET /auth/v1/health -> 200 with JSON that is not GoTrue's health document, "
                f"so SUPABASE_URL does not name an auth origin: {flat[:120]}"
            )
        else:
            ok(f"GET /auth/v1/health -> 200 from GoTrue {document.get('version') or ''}".rstrip())
    elif status == 401:
        fail(f"GET /auth/v1/health -> 401, the anon key was rejected by this project: {flat}")
    elif status == 0:
        fail(f"GET /auth/v1/health did not complete: {flat}")
    else:
        fail(f"GET /auth/v1/health -> {status}, expected 200: {flat}")

    print()
    if failures:
        print(f"PREFLIGHT FAILED: {len(failures)} check(s) did not pass")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("PREFLIGHT PASSED: the Supabase configuration resolves and its keys belong to it")
    if notes:
        print(f"({len(notes)} check(s) skipped as not applicable to this deployment shape)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
