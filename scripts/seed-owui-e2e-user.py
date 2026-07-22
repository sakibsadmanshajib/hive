#!/usr/bin/env python3
"""Idempotently provision the OWUI e2e test user, tenant, and membership.

Run before the phase-19 OWUI nightly Playwright suite so OWUI_E2E_EMAIL /
OWUI_E2E_PASSWORD never need to be hand-managed GitHub secrets. Safe to
re-run: the tenant and membership rows are upserted, the GoTrue user is
found-or-created, and its password is always rotated to a fresh random
value so no run reuses a prior credential.

Also mints a real, resolvable Hive API key to use as OWUI_SHIM_KEY. OWUI's
own GET /v1/models connection probe sends OPENAI_API_KEY with no request
body, so edge-api's OWUIUnwrap middleware (which only swaps in a per-user
JWT when the body carries __metadata) can never rewrite it -- see
deploy/docker/pipelines/hive_jwt_forward.py's inlet comment. A random,
unregistered shim value therefore always 401s model listing even on a
healthy stack (run 28685935882). A real "hk_"-prefixed key routes straight
through the existing API-key path for that bodyless call. This key lives on
its own throwaway "owui-e2e-shim" billing account (the older accounts/
api_keys schema, unrelated to the tenants/tenant_users rows below) with
allow_all_models=true -- listing is not billable, so there is no reason to
depend on default-policy-group alias membership. Rotated every run the same
way the GoTrue password is.

Also pushes the minted SHIM_KEY straight into Open WebUI's own persisted
OpenAI-connection config (POST /openai/config/update), not just .env and
the container's OPENAI_API_KEY env var. OWUI only seeds that config from
OPENAI_API_KEY on a volume's first boot -- every later container recreate
on the same volume keeps the OLD key even after .env and the env var move
on, and the chat UI silently shows "No models available" even though the
new key works fine directly against edge-api. Confirmed live 2026-07-22.
This sync step is best-effort: set OWUI_BASE_URL, OWUI_ADMIN_EMAIL, and
OWUI_ADMIN_PASSWORD to enable it, or it logs a warning to stderr and
skips (never fatal -- minting the Supabase-side credentials below is
this script's real job).

Required env: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY
Optional env (OWUI config sync): OWUI_BASE_URL (default
http://localhost:3003), OWUI_ADMIN_EMAIL, OWUI_ADMIN_PASSWORD,
EDGE_API_URL_FOR_OWUI (default http://edge-api:8080/v1 -- the docker-network
hostname OWUI's own backend dials out to; override for a remote OWUI setup)

Prints exactly three lines to stdout (and nothing else):
  EMAIL=<email>
  PASSWORD=<password>
  SHIM_KEY=<hk_ api key>
Everything else (progress, errors) goes to stderr.
"""
import base64
import hashlib
import json
import os
import secrets
import string
import sys
import urllib.error
import urllib.parse
import urllib.request

TENANT_SLUG = "owui-e2e"
TENANT_NAME = "OWUI E2E"
SHIM_ACCOUNT_SLUG = "owui-e2e-shim"
SHIM_ACCOUNT_NAME = "OWUI E2E Shim"
SHIM_KEY_NICKNAME = "owui-e2e-shim-key"
# ponytail: no Go code branches on tenants.deployment today (grep confirmed
# clean). ENTERPRISE_EDGE picked as the closer conceptual fit for a
# self-hosted OWUI chat front-end; revisit if that ever becomes load-bearing.
TENANT_DEPLOYMENT = "ENTERPRISE_EDGE"
# .invalid is an IANA-reserved TLD (RFC 2606) meant for exactly this use;
# verified live against this project's GoTrue instance that it accepts the
# format (2026-07-03 probe, see PR description).
USER_EMAIL = "owui-e2e@hive-e2e.invalid"
MEMBER_ROLE = "MEMBER"
MEMBER_STATUS = "ACTIVE"


def env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        print(f"error: {name} is not set", file=sys.stderr)
        sys.exit(1)
    return value


def request(base, headers, method, path, body=None, params=None, prefer=None):
    url = base + path
    if params:
        url += "?" + urllib.parse.urlencode(params)
    data = json.dumps(body).encode() if body is not None else None
    req_headers = dict(headers)
    if prefer:
        req_headers["Prefer"] = prefer
    req = urllib.request.Request(url, data=data, method=method, headers=req_headers)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except json.JSONDecodeError:
            print(f"error: {method} {path} -> {e.code}: {raw[:300]!r}", file=sys.stderr)
            sys.exit(1)


def random_password() -> str:
    # Prefix guarantees upper/lower/digit/symbol classes regardless of the
    # random draw; total length (28) clears any realistic GoTrue min-length
    # policy with room to spare, well under bcrypt's 72-byte limit.
    alphabet = string.ascii_letters + string.digits + "!@#$%^&*-_"
    return "Aa1!" + "".join(secrets.choice(alphabet) for _ in range(24))


def random_api_key() -> tuple[str, str, str]:
    """Mirrors generateSecret() in apps/control-plane/internal/apikeys/service.go:
    32 random bytes, base64url (no padding), "hk_" prefix, sha256 hex hash.
    Returns (raw_secret, token_hash, redacted_suffix)."""
    encoded = base64.urlsafe_b64encode(secrets.token_bytes(32)).rstrip(b"=").decode()
    raw_secret = "hk_" + encoded
    token_hash = hashlib.sha256(raw_secret.encode()).hexdigest()
    return raw_secret, token_hash, raw_secret[-6:]


# Internal docker-network hostname edge-api resolves to inside the OWUI
# container. Not host-reachable -- unlike OWUI_BASE_URL below, which this
# script itself calls from the host, this is where OWUI's own backend
# dials out to for chat completions once the config below is saved.
OWUI_UPSTREAM_BASE_URL = "http://edge-api:8080/v1"


def merge_owui_config(existing: dict, upstream_url: str, raw_secret: str) -> dict:
    """Merge raw_secret into an existing GET /openai/config response,
    preserving every other configured connection untouched. OWUI's own
    POST /openai/config/update REPLACES the whole collection -- it does
    not merge server-side. Find-or-append here, or any other
    OpenAI-compatible connection an admin configured by hand through
    OWUI's own UI gets silently wiped by this script."""
    base_urls = list(existing.get("OPENAI_API_BASE_URLS") or [])
    api_keys = list(existing.get("OPENAI_API_KEYS") or [])
    configs = dict(existing.get("OPENAI_API_CONFIGS") or {})
    if upstream_url in base_urls:
        idx = base_urls.index(upstream_url)
        while len(api_keys) <= idx:  # defensive: OWUI keeps these aligned
            api_keys.append("")
        api_keys[idx] = raw_secret
    else:
        idx = len(base_urls)
        base_urls.append(upstream_url)
        api_keys.append(raw_secret)
    configs[str(idx)] = {"enable": True}
    return {
        "ENABLE_OPENAI_API": True,
        "OPENAI_API_BASE_URLS": base_urls,
        "OPENAI_API_KEYS": api_keys,
        "OPENAI_API_CONFIGS": configs,
    }


def owui_request(base, headers, method, path, body=None):
    """Same shape as request() above but never sys.exit on error --
    sync_owui_config below is best-effort and must always fall through
    to this script's real job (minting the Supabase-side credentials)."""
    url = base + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers=dict(headers))
    with urllib.request.urlopen(req, timeout=10) as resp:
        raw = resp.read()
        return resp.status, (json.loads(raw) if raw else None)


def sync_owui_config(raw_secret: str) -> None:
    """Best-effort: sign into OWUI as an admin, fetch its existing
    persisted OpenAI config, merge raw_secret into it (preserving any
    other configured connection), and push the merged result back. Logs
    to stderr either way, never raises, never sys.exit -- see module
    docstring for why. By the time this runs the OLD key is already
    deleted from Supabase (step 5 above), so a failure here leaves OWUI
    pointed at a dead key -- the warn() below spells out manual recovery
    so that never sits silent."""
    base = os.environ.get("OWUI_BASE_URL", "http://localhost:3003").rstrip("/")
    email = os.environ.get("OWUI_ADMIN_EMAIL", "").strip()
    password = os.environ.get("OWUI_ADMIN_PASSWORD", "").strip()
    upstream_url = os.environ.get("EDGE_API_URL_FOR_OWUI", OWUI_UPSTREAM_BASE_URL).rstrip("/")

    def warn(reason: str) -> None:
        print(f"owui config sync skipped: {reason}", file=sys.stderr)
        print(
            'owui config sync FAILED: chat UI will show "No models '
            'available" until this is fixed by hand. Recover with:\n'
            f"  TOKEN=$(curl -s -X POST {base}/api/v1/auths/signin "
            '-H "Content-Type: application/json" '
            '-d \'{"email":"<OWUI_ADMIN_EMAIL>","password":"<OWUI_ADMIN_PASSWORD>"}\' '
            "| python3 -c \"import sys,json;print(json.load(sys.stdin)['token'])\")\n"
            f"  curl -s -X POST {base}/openai/config/update "
            '-H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" '
            '-d \'{"ENABLE_OPENAI_API":true,"OPENAI_API_BASE_URLS":["' + upstream_url + '"],'
            '"OPENAI_API_KEYS":["' + raw_secret + '"],"OPENAI_API_CONFIGS":{"0":{"enable":true}}}\'\n'
            "  (the recipe above REPLACES OWUI's whole connection list -- fine for a "
            "single-connection demo box, but check GET " + base + "/openai/config first "
            "if other connections might exist)",
            file=sys.stderr,
        )

    # No hardcoded credential defaults. Local/demo test account already
    # documented in scripts/seed-demo-owner.py's header comment
    # (asdas@asdas.sda / asdas) if a caller wants to set these explicitly.
    if not email or not password:
        print("owui config sync skipped: OWUI_ADMIN_EMAIL/OWUI_ADMIN_PASSWORD not set", file=sys.stderr)
        return
    try:
        status, body = owui_request(
            base, {"Content-Type": "application/json"}, "POST",
            "/api/v1/auths/signin", {"email": email, "password": password},
        )
        token = body.get("token") if isinstance(body, dict) else None
        if status != 200 or not token:
            warn(f"signin failed: {status} {body}")
            return

        auth_headers = {"Content-Type": "application/json", "Authorization": f"Bearer {token}"}
        status, existing = owui_request(base, auth_headers, "GET", "/openai/config")
        if status != 200 or not isinstance(existing, dict):
            warn(f"config fetch failed: {status} {existing}")
            return

        status, body = owui_request(
            base, auth_headers, "POST", "/openai/config/update",
            merge_owui_config(existing, upstream_url, raw_secret),
        )
        if status != 200:
            warn(f"config update failed: {status} {body}")
            return
        print("owui config sync: ok", file=sys.stderr)
    except urllib.error.HTTPError as e:
        raw = e.read()
        warn(f"{e.code} {raw[:300]!r}")
    except (urllib.error.URLError, OSError, json.JSONDecodeError) as e:
        warn(str(e))


def main() -> None:
    supabase_url = env("SUPABASE_URL").rstrip("/")
    service_key = env("SUPABASE_SERVICE_ROLE_KEY")
    headers = {
        "Authorization": f"Bearer {service_key}",
        "apikey": service_key,
        "Content-Type": "application/json",
    }
    rest = supabase_url + "/rest/v1"
    gotrue = supabase_url + "/auth/v1"

    # 1. Upsert the tenant (service role bypasses RLS).
    status, body = request(
        rest, headers, "POST", "/tenants",
        body={"slug": TENANT_SLUG, "name": TENANT_NAME, "deployment": TENANT_DEPLOYMENT},
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: tenant upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    tenant_id = body[0]["id"]

    # 2. Find-or-create the GoTrue user. `filter=<email>` does an exact
    # server-side match (verified live; the `email=` param is NOT
    # supported and 500s on this GoTrue version).
    status, body = request(gotrue, headers, "GET", "/admin/users", params={"filter": USER_EMAIL})
    if status != 200:
        print(f"error: user lookup failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    existing = next(
        (u for u in body.get("users", []) if u.get("email", "").lower() == USER_EMAIL.lower()),
        None,
    )

    password = random_password()
    user_metadata = {"selected_tenant_id": tenant_id}

    if existing is None:
        status, body = request(
            gotrue, headers, "POST", "/admin/users",
            body={
                "email": USER_EMAIL,
                "password": password,
                "email_confirm": True,
                "user_metadata": user_metadata,
            },
        )
        if status not in (200, 201):
            print(f"error: user create failed: {status} {body}", file=sys.stderr)
            sys.exit(1)
        user_id = body["id"]
    else:
        user_id = existing["id"]
        # GoTrue's admin updateUserById MERGES user_metadata (verified
        # live), so this only ever adds/refreshes selected_tenant_id.
        # Password is rotated unconditionally on every run.
        status, body = request(
            gotrue, headers, "PUT", f"/admin/users/{user_id}",
            body={"password": password, "user_metadata": user_metadata},
        )
        if status != 200:
            print(f"error: user update failed: {status} {body}", file=sys.stderr)
            sys.exit(1)

    # 3. Upsert tenant membership.
    status, body = request(
        rest, headers, "POST", "/tenant_users",
        body={
            "tenant_id": tenant_id,
            "user_id": user_id,
            "role": MEMBER_ROLE,
            "status": MEMBER_STATUS,
        },
        params={"on_conflict": "tenant_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: membership upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 4. Upsert the throwaway shim billing account (older accounts/api_keys
    # schema -- separate from the tenants/tenant_users rows above).
    status, body = request(
        rest, headers, "POST", "/accounts",
        body={
            "slug": SHIM_ACCOUNT_SLUG,
            "display_name": SHIM_ACCOUNT_NAME,
            "account_type": "business",
            "owner_user_id": user_id,
        },
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: shim account upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    shim_account_id = body[0]["id"]

    # 5. Rotate the shim API key: drop any previous key(s) for this account
    # (cascades to their policy rows) then mint a fresh one, same rotate-
    # every-run posture as the GoTrue password above.
    status, body = request(
        rest, headers, "DELETE", "/api_keys",
        params={"account_id": f"eq.{shim_account_id}"},
    )
    if status not in (200, 204):
        print(f"error: shim key cleanup failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    raw_secret, token_hash, redacted_suffix = random_api_key()
    status, body = request(
        rest, headers, "POST", "/api_keys",
        body={
            "account_id": shim_account_id,
            "nickname": SHIM_KEY_NICKNAME,
            "token_hash": token_hash,
            "redacted_suffix": redacted_suffix,
            "status": "active",
            "created_by_user_id": user_id,
        },
        prefer="return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: shim key create failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    shim_key_id = body[0]["id"]

    status, body = request(
        rest, headers, "POST", "/api_key_policies",
        body={"api_key_id": shim_key_id, "allow_all_models": True},
    )
    if status not in (200, 201, 204):
        print(f"error: shim key policy create failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 6. Best-effort: keep OWUI's own persisted config in sync with the
    # key just minted above (see module docstring). Never touches the
    # stdout contract below.
    sync_owui_config(raw_secret)

    print(f"EMAIL={USER_EMAIL}")
    print(f"PASSWORD={password}")
    print(f"SHIM_KEY={raw_secret}")


if __name__ == "__main__":
    main()
