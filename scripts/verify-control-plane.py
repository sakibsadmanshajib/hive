#!/usr/bin/env python3
"""Assert every control-plane surface answers correctly on a running stack.

CI cannot cover this: the control-plane's auth middleware resolves each bearer
against live Supabase GoTrue, and most reads need real rows. So the surfaces
below are only ever exercised against a deployed stack, which is how two routes
shipped structurally broken (an admin mux mounted under a prefix its ServeMux
patterns could never match, and a tenant id handed to an account-scoped
ownership predicate). Both answered every request with 404 and 500 respectively
while every unit test passed. This script is the check that fails on that class.

It asserts status codes rather than printing them, and exits non-zero on the
first surface that answers unexpectedly.

Usage (from the repo root, on a host that can reach the stack):

    set -a; . .env; set +a
    python3 scripts/verify-control-plane.py

Required env: SUPABASE_URL, SUPABASE_ANON_KEY, SUPABASE_SERVICE_ROLE_KEY,
CONTROL_PLANE_INTERNAL_TOKEN.

Optional env:
    HIVE_CONTROL_PLANE_URL   default http://localhost:8081
    HIVE_EDGE_API_URL        default http://localhost:8080
    HIVE_VERIFY_EMAIL        default demo@hive-demo.invalid
    HIVE_VERIFY_TENANT_SLUG  default hive-demo

The caller identified by HIVE_VERIFY_EMAIL must already exist and be both a
platform admin and an OWNER of the tenant; scripts/seed-demo-owner.py
provisions exactly that. Its password is rotated through the GoTrue admin API
on each run, so no credential is ever passed on the command line or printed.
"""
import hashlib
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

CP = os.environ.get("HIVE_CONTROL_PLANE_URL", "http://localhost:8081").rstrip("/")
EDGE = os.environ.get("HIVE_EDGE_API_URL", "http://localhost:8080").rstrip("/")
EMAIL = os.environ.get("HIVE_VERIFY_EMAIL", "demo@hive-demo.invalid")
TENANT_SLUG = os.environ.get("HIVE_VERIFY_TENANT_SLUG", "hive-demo")

failures: list[str] = []


def env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(f"error: {name} is not set")
    return value


def http(method, url, headers=None, body=None, timeout=120):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode(errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")
    except Exception as e:  # noqa: BLE001 — a transport failure is a result too
        return 0, f"<transport error: {type(e).__name__}: {e}>"


def check(method, path, headers, want, body=None, base=None):
    """Assert one request's status is in `want`. Returns the response body."""
    base = base or CP
    status, raw = http(method, base + path, headers, body)
    flat = " ".join(raw.split())
    ok = status in want
    print(f"  [{'PASS' if ok else 'FAIL'}] {method:6} {path:56} -> {status} (want {'/'.join(map(str, want))})")
    if not ok:
        print(f"           body: {flat[:300]}")
        failures.append(f"{method} {path} -> {status}, want {want}")
    return raw


def main() -> None:
    supabase = env("SUPABASE_URL").rstrip("/")
    anon = env("SUPABASE_ANON_KEY")
    service = env("SUPABASE_SERVICE_ROLE_KEY")
    internal_token = env("CONTROL_PLANE_INTERNAL_TOKEN")
    svc_h = {"Authorization": f"Bearer {service}", "apikey": service, "Content-Type": "application/json"}

    print(f"control-plane {CP} | edge-api {EDGE} | caller {EMAIL}")

    print("\n== liveness ==")
    check("GET", "/health", {}, (200,))

    # Resolve the caller and mint a session. The password is rotated rather than
    # supplied so this script never needs a stored credential.
    status, raw = http(
        "GET", f"{supabase}/auth/v1/admin/users?" + urllib.parse.urlencode({"filter": EMAIL}), svc_h
    )
    if status != 200:
        sys.exit(f"error: user lookup failed: {status} {raw[:200]}")
    user = next((u for u in json.loads(raw).get("users", []) if u.get("email", "").lower() == EMAIL.lower()), None)
    if user is None:
        sys.exit(f"error: {EMAIL} does not exist; run scripts/seed-demo-owner.py first")

    password = "Verify-" + os.urandom(12).hex() + "-aA1!"
    status, raw = http("PUT", f"{supabase}/auth/v1/admin/users/{user['id']}", svc_h, {"password": password})
    if status != 200:
        sys.exit(f"error: password rotate failed: {status} {raw[:200]}")
    status, raw = http(
        "POST", f"{supabase}/auth/v1/token?grant_type=password",
        {"apikey": anon, "Content-Type": "application/json"},
        {"email": EMAIL, "password": password},
    )
    if status != 200:
        sys.exit(f"error: sign-in failed: {status} {raw[:200]}")
    auth = {"Authorization": f"Bearer {json.loads(raw)['access_token']}", "Content-Type": "application/json"}
    internal = {"X-Internal-Token": internal_token, "Content-Type": "application/json"}

    status, raw = http(
        "GET", f"{supabase}/rest/v1/tenants?select=id&slug=eq.{urllib.parse.quote(TENANT_SLUG)}", svc_h
    )
    rows = json.loads(raw) if status == 200 else []
    if not rows:
        sys.exit(f"error: tenant slug {TENANT_SLUG!r} not found")
    tenant = rows[0]["id"]
    print(f"caller resolved, tenant {tenant}")

    print("\n== account, profile and billing reads (session JWT) ==")
    for path in (
        "/api/v1/viewer",
        "/api/v1/accounts/current/members",
        "/api/v1/accounts/current/profile",
        "/api/v1/accounts/current/billing-profile",
        "/api/v1/accounts/current/credits/balance",
        "/api/v1/accounts/current/credits/ledger",
        "/api/v1/accounts/current/invoices",
        "/api/v1/accounts/current/budget",
        "/api/v1/accounts/current/analytics/usage",
        "/api/v1/accounts/current/analytics/spend",
        "/api/v1/accounts/current/analytics/errors",
        "/api/v1/accounts/current/request-attempts",
        "/api/v1/accounts/current/usage-events",
        "/api/v1/accounts/current/checkout/rails",
    ):
        check("GET", path, auth, (200,))

    print("\n== catalog ==")
    check("GET", "/api/v1/catalog/models", auth, (200,))
    check("GET", "/api/v1/catalog/models", {}, (200,))

    print("\n== platform-admin surfaces ==")
    # A prefix mismatch here answers with Go's default "404 page not found",
    # which is exactly what these assertions exist to catch.
    check("GET", "/api/v1/admin/feature-gates", auth, (200,))
    check("GET", "/api/v1/admin/providers", auth, (200,))
    check("GET", "/api/v1/admin/marketplace", auth, (200,))

    print("\n== tenant-owner surfaces ==")
    # 404 means routed and authorized with nothing stored yet; 500 means the
    # ownership predicate could not resolve the tenant id at all.
    check("GET", f"/api/v1/egress-policy/{tenant}", auth, (200, 404))

    print("\n== api-key lifecycle and the full key auth chain ==")
    raw = check("POST", "/api/v1/accounts/current/api-keys", auth, (201,), {"nickname": "verify-control-plane"})
    key_id = secret = ""
    try:
        created = json.loads(raw)
        key_id, secret = created.get("id", ""), created.get("secret", "")
    except json.JSONDecodeError:
        failures.append("api-key create response was not JSON")

    check("GET", "/api/v1/accounts/current/api-keys", auth, (200,))
    if key_id:
        check("GET", f"/api/v1/accounts/current/api-keys/{key_id}", auth, (200,))
        check("GET", f"/api/v1/accounts/current/api-keys/{key_id}/limits", auth, (200,))
    if secret:
        check("POST", "/internal/apikeys/resolve", internal, (200,),
              {"token_hash": hashlib.sha256(secret.encode()).hexdigest()})
        key_auth = {"Authorization": f"Bearer {secret}", "Content-Type": "application/json"}
        check("GET", "/v1/models", key_auth, (200,), base=EDGE)
        check("POST", "/v1/chat/completions", key_auth, (200,),
              {"model": "hive-default", "messages": [{"role": "user", "content": "say pong"}],
               "max_tokens": 16}, base=EDGE)
    else:
        failures.append("api-key create returned no secret, key auth chain not exercised")

    print("\n== internal service-to-service surfaces ==")
    check("GET", "/internal/catalog/snapshot", internal, (200,))
    check("GET", f"/internal/featuregate/{tenant}", internal, (200,))
    check("GET", "/internal/providers", internal, (200,))
    check("GET", f"/internal/marketplace/{tenant}/mcp-servers", internal, (200,))
    check("GET", f"/internal/egress-policy/{tenant}", internal, (200,))

    print("\n== negative auth cases ==")
    check("GET", "/internal/catalog/snapshot", {"X-Internal-Token": "wrong-token"}, (401,))
    check("GET", "/internal/catalog/snapshot", {}, (401,))
    check("GET", "/api/v1/accounts/current/credits/balance", {"Authorization": "Bearer not-a-jwt"}, (401,))
    check("GET", "/api/v1/accounts/current/credits/balance", {}, (401,))
    check("GET", "/api/v1/admin/providers", {}, (401,))
    check("GET", f"/api/v1/egress-policy/{tenant}", {}, (401,))

    print("\n== cleanup ==")
    if key_id:
        check("POST", f"/api/v1/accounts/current/api-keys/{key_id}/revoke", auth, (200, 204), {})

    print()
    if failures:
        print(f"FAILED: {len(failures)} surface(s) answered unexpectedly")
        for f in failures:
            print(f"  - {f}")
        sys.exit(1)
    print("PASSED: every control-plane surface answered as expected")


if __name__ == "__main__":
    main()
