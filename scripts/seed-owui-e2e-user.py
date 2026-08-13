#!/usr/bin/env python3
"""Idempotently provision the OWUI e2e test user, tenant, and membership.

Run before the phase-19 OWUI nightly Playwright suite so OWUI_E2E_EMAIL /
OWUI_E2E_PASSWORD never need to be hand-managed GitHub secrets. Safe to
re-run: the tenant and membership rows are upserted and the GoTrue user is
found-or-created.

PASSWORDS ARE NOT ROTATED. This script used to overwrite both fixture
users' passwords on every run, which is the shape docs/live-test-auth.md
forbids: the control-plane resolves every bearer against GoTrue per
request, so rotating an account revokes the sessions every concurrent run
is holding on it, and this script runs automatically from
.github/workflows/owui-nightly.yml on a schedule, on dispatch, and on any
pull request labelled run-owui-e2e (whose concurrency group is keyed on the
ref, so it does not join the scheduled run's group). An existing account is
now left alone, and a PASSWORD line is printed only for a password this run
actually set. See password_to_set.

OWUI_E2E_RUN_KEY is how the nightly keeps working under that rule: it
namespaces both fixture addresses per run (foo@x -> foo+key@x), so every
run provisions its own users, meets no existing account, and shares no
credential with any other run. Same idea as E2E_RUN_KEY in the web-console
fixture seeder. Left unset, the script uses the shared addresses below and
will refuse to overwrite their passwords.

Also mints a real, resolvable Hive API key to use as OWUI_SHIM_KEY. OWUI's
own GET /v1/models connection probe sends OPENAI_API_KEY with no request
body, so edge-api's OWUIUnwrap middleware (which only swaps in a per-user
JWT when the body carries __metadata) can never rewrite it -- see
deploy/docker/pipelines/hive_jwt_forward.py's inlet comment. A random,
unregistered shim value therefore always 401s model listing even on a
healthy stack (run 28685935882), and takes OWUI's document-RAG embeddings
and text-to-speech down with it, since those authenticate as that key and
nothing else. A real "hk_"-prefixed key routes straight through the
existing API-key path for all three. This key lives on its own throwaway
billing account (the older accounts/api_keys schema, unrelated to the
tenants/tenant_users rows below) with allow_all_models=true.

TENANT MAPPING. That account is also mapped to the tenant in
public.tenant_billing_accounts, because edge-api resolves an API key's
tenant from exactly that row and fails closed without it: an active,
allow-all-models key on an unmapped account answers 403
account_not_provisioned on model listing, RAG embeddings and TTS alike
(issue #717). The mapping is 1:1 in both directions, so a deployment that
passes its own --account-slug must pass its own --tenant-slug as well;
see provision_billing_mapping.

ROTATION AND ACCOUNT SCOPING. Every run mints a fresh key and revokes the
account's previous ones: a key's raw secret only exists in the response to
its own mint, so a re-run cannot hand back an existing key's value. (The
GoTrue passwords above no longer rotate. An API key is not a login: nothing
holds a session against it, and the revocation here is deferred until every
configured consumer carries the replacement.) That makes the
account boundary load-bearing. --account-slug defaults to the CI account,
which the nightly OWUI job rotates on a schedule; a long-lived deployment
MUST pass its own slug (for example --account-slug owui-shim-demo-box) so
a scheduled CI run can never revoke the key that deployment depends on.
Revocation of the old keys is also deferred until every configured
consumer has been updated (see below), so a failed sync leaves the
previous key working rather than stranding the deployment on a dead one.

CONSUMER SYNC. Both consumers of this value are updated before the old key
is revoked:
  * --env-file <path> rewrites OWUI_SHIM_KEY in a compose .env in place
    (atomic replace, mode preserved), so a container recreate or host
    reboot does not resurrect the old value.
  * OWUI_BASE_URL plus OWUI_ADMIN_TOKEN (or OWUI_ADMIN_EMAIL and
    OWUI_ADMIN_PASSWORD) push the key into Open WebUI's own persisted
    OpenAI-connection config (POST /openai/config/update). OWUI only seeds
    that config from OPENAI_API_KEY on a volume's first boot -- every later
    container recreate on the same volume keeps the OLD key even after .env
    and the env var move on, and the chat UI silently shows "No models
    available" even though the new key works fine directly against
    edge-api. Confirmed live 2026-07-22. Prefer OWUI_ADMIN_TOKEN: an
    OAuth-only Open WebUI has no password-authenticable admin at all.
Configure neither and the script only mints, which is what CI wants (it
writes a fresh .env.ci and boots a fresh OWUI container every run). If a
consumer IS configured and its update fails, the old keys are left active
and the script exits non-zero.

Required env: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY
Optional env (fixture identity): OWUI_E2E_RUN_KEY (namespace both fixture
addresses for this run), OWUI_E2E_PASSWORD and OWUI_E2E_BOOTSTRAP_PASSWORD
(write these passwords deliberately, including onto an existing account)
Optional env (OWUI config sync): OWUI_BASE_URL (default
http://localhost:3003), OWUI_ADMIN_TOKEN, OWUI_ADMIN_EMAIL,
OWUI_ADMIN_PASSWORD, EDGE_API_URL_FOR_OWUI (default
http://edge-api:8080/v1 -- the docker-network hostname OWUI's own backend
dials out to; override for a remote OWUI setup)

Also provisions a second, throwaway "bootstrap" tenant member
(BOOTSTRAP_EMAIL below). Open WebUI auto-promotes the very first user it
ever sees to admin, bypassing OAUTH_ALLOWED_ROLES/OAUTH_ROLES_CLAIM
entirely -- and the OWUI container in the nightly job is freshly created
every run, so without a bootstrap login the real fixture user below would
always land as that unconditional first-user admin and never actually
exercise the owui_role allow-list gate that PR #451 fixed (every OWUI
container start is a clean slate: no persisted volume carries a prior user
across runs). The Playwright setup below signs the bootstrap user in and out
first so the real fixture user is provably the second OWUI account, the same
position every real tenant's OWNER is in once the OWUI instance already has
at least one user.

Prints to stdout (and nothing else):
  EMAIL=<email>
  PASSWORD=<password>             only when this run set that password
  SHIM_KEY=<hk_ api key>
  BOOTSTRAP_EMAIL=<email>
  BOOTSTRAP_PASSWORD=<password>   only when this run set that password
Everything else (progress, errors, and the reason a PASSWORD line is
missing) goes to stderr.
"""
import argparse
import base64
import datetime
import hashlib
import json
import os
import secrets
import stat
import string
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request

TENANT_SLUG = "owui-e2e"
TENANT_NAME = "OWUI E2E"
# Default account is CI's. The nightly OWUI job rotates it on a schedule, so a
# long-lived deployment must pass its own --account-slug (see module docstring).
SHIM_ACCOUNT_SLUG = "owui-e2e-shim"
SHIM_ACCOUNT_NAME = "OWUI E2E Shim"
SHIM_KEY_NICKNAME = "owui-e2e-shim-key"
SHIM_ENV_VAR = "OWUI_SHIM_KEY"
# ponytail: no Go code branches on tenants.deployment today (grep confirmed
# clean). ENTERPRISE_EDGE picked as the closer conceptual fit for a
# self-hosted OWUI chat front-end; revisit if that ever becomes load-bearing.
TENANT_DEPLOYMENT = "ENTERPRISE_EDGE"
# .invalid is an IANA-reserved TLD (RFC 2606) meant for exactly this use;
# verified live against this project's GoTrue instance that it accepts the
# format (2026-07-03 probe, see PR description).
USER_EMAIL = "owui-e2e@hive-e2e.invalid"
# OWNER, not MEMBER: every self-serve tenant's first-ever user is OWNER
# (see PR #451), and that is exactly the role OWUI's OAUTH_ALLOWED_ROLES
# gate rejected before the owui_role claim fix. Testing OWNER here is what
# actually exercises the incident's real path; MEMBER (the prior value)
# never touched it.
USER_ROLE = "OWNER"
MEMBER_STATUS = "ACTIVE"
# Bootstrap identity: see module docstring. Role doesn't matter -- it only
# needs to complete one OAuth login so it, not the real fixture user,
# absorbs Open WebUI's unconditional first-user-becomes-admin promotion.
BOOTSTRAP_EMAIL = "owui-e2e-bootstrap@hive-e2e.invalid"
BOOTSTRAP_ROLE = "MEMBER"


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


def password_to_set(user_exists: bool, env_password: str, new_user_password: str) -> str | None:
    """The password this run should write, or None to leave it untouched.

    This script used to rotate unconditionally, on the reasoning that no run
    should reuse a prior credential. That is wrong for an account other runs
    are holding sessions on. The control-plane resolves every bearer against
    GoTrue on each request, so a rotation revokes those sessions mid-run, and
    this script is invoked automatically by .github/workflows/owui-nightly.yml
    on a schedule, on dispatch, and on any pull request labelled run-owui-e2e.
    The workflow's concurrency group is keyed on the ref, so a labelled-pull-
    request run and the scheduled run do not join the same group and can rotate
    each other's account mid-flight. docs/live-test-auth.md forbids exactly
    this shape.

    So the default is to leave an existing account alone, and a caller that
    genuinely wants to set the password says so through an environment
    variable. A brand-new account still gets new_user_password: there is no
    session to break and no credential to preserve. Generation stays in the
    caller so this selector is pure and directly assertable.

    Same helper, same semantics as scripts/seed-demo-owner.py.
    """
    env_password = env_password.strip()
    if env_password:
        return env_password
    if not user_exists:
        return new_user_password
    return None


# A key older than this is nobody's live key: the nightly job's own timeout is
# 30 minutes, and a deployment that needs to keep one longer has its own
# billing account through --account-slug (see the module docstring).
STALE_KEY_HOURS = 6


def stale_key_cutoff_iso(now: datetime.datetime | None = None) -> str:
    """The created_at boundary for revoking previously minted shim keys.

    Bounding the delete by age rather than by identity is what stops two
    overlapping runs from revoking each other's key mid-flight."""
    now = now or datetime.datetime.now(datetime.timezone.utc)
    return (now - datetime.timedelta(hours=STALE_KEY_HOURS)).isoformat()


def sweep_stale_fixture_users(gotrue, headers, run_key: str) -> None:
    """Delete the run-scoped fixture users left behind by earlier runs.

    Every run now provisions its own users rather than rotating shared ones,
    which trades a live incident for a slow leak: two permanent GoTrue users
    per run, one of them an OWNER of the owui-e2e tenant. This is the same
    trade, and the same answer, as sweepStaleFixtureRuns in
    apps/web-console/tests/e2e/support/e2e-fixture-seed.mjs.

    Best effort by design. A sweep failure must never fail a run that has
    already provisioned what it needs, so every error here is a note on stderr.
    Only addresses this script itself derives are considered: the two bases,
    each with a "+" tag, and never the unnamespaced shared address.
    """
    if not run_key:
        return
    cutoff = stale_key_cutoff_iso()
    prefixes = tuple(f"{email.split('@')[0].lower()}+" for email in (USER_EMAIL, BOOTSTRAP_EMAIL))
    domains = tuple(email.split("@")[1].lower() for email in (USER_EMAIL, BOOTSTRAP_EMAIL))

    # ponytail: one page, no pagination. The sweep is best effort and the leak
    # it clears is two users per nightly, so a single page of 1000 is ample
    # today. The ceiling is real though: on a project whose auth.users grows
    # past that page, stale fixture users fall outside it and are never swept,
    # silently, because this function never fails a run. Upgrade path when that
    # matters: loop `page` until a short page comes back, and keep the same
    # both-halves address match so a real account can never be a candidate.
    status, body = request(gotrue, headers, "GET", "/admin/users", params={"per_page": "1000"})
    if status != 200 or not isinstance(body, dict):
        print(f"note: fixture sweep skipped (user listing returned {status})", file=sys.stderr)
        return

    swept = 0
    for user in body.get("users", []):
        email = (user.get("email") or "").lower()
        local, _, domain = email.partition("@")
        # Both halves must match, so a real customer address that happens to
        # start with the same word is never a candidate.
        if domain not in domains or not local.startswith(prefixes):
            continue
        # Never this run's own users, and never the shared base address, which
        # carries no "+" tag at all and is what the prefixes above require.
        if f"+{run_key}@" in email:
            continue
        if (user.get("created_at") or "") >= cutoff:
            continue
        del_status, del_body = request(gotrue, headers, "DELETE", f"/admin/users/{user['id']}")
        if del_status not in (200, 204):
            print(f"note: fixture sweep could not delete {email}: {del_status} {del_body}", file=sys.stderr)
            continue
        swept += 1
    if swept:
        print(f"fixture sweep: removed {swept} stale run-scoped user(s)", file=sys.stderr)


def with_run_key(email: str, run_key: str) -> str:
    """Namespace an address per run (foo@x -> foo+key@x), or return it
    unchanged when run_key is empty.

    This is what keeps the guard above from simply breaking the nightly. CI
    passes one key per job attempt, so every run provisions its own users, no
    run ever meets an existing account, and no shared credential exists to
    rotate in the first place. Same idea and same shape as E2E_RUN_KEY in
    apps/web-console/tests/e2e/support/e2e-fixture-seed.mjs.
    """
    run_key = run_key.strip()
    if not run_key:
        return email
    local, at, domain = email.partition("@")
    return f"{local}+{run_key}{at}{domain}"


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


def sync_owui_config(raw_secret: str) -> bool | None:
    """Sign into OWUI as an admin, fetch its existing persisted OpenAI
    config, merge raw_secret into it (preserving any other configured
    connection), and push the merged result back.

    Returns True on success, False on failure, or None when no OWUI admin
    credential is configured (CI's case, where OWUI is recreated from
    .env.ci every run and has no persisted config to correct). The caller
    holds off revoking the previous key until this returns non-False, so a
    failure here never strands OWUI on a dead key."""
    base = os.environ.get("OWUI_BASE_URL", "http://localhost:3003").rstrip("/")
    token = os.environ.get("OWUI_ADMIN_TOKEN", "").strip()
    email = os.environ.get("OWUI_ADMIN_EMAIL", "").strip()
    password = os.environ.get("OWUI_ADMIN_PASSWORD", "").strip()
    upstream_url = os.environ.get("EDGE_API_URL_FOR_OWUI", OWUI_UPSTREAM_BASE_URL).rstrip("/")

    def warn(reason: str) -> None:
        print(f"owui config sync FAILED: {reason}", file=sys.stderr)
        print(
            'owui config sync FAILED: chat UI will show "No models available" '
            "until this is fixed. The previous shim key was NOT revoked, so the "
            "deployment keeps working on it; re-run once Open WebUI is reachable, "
            "or push the new key by hand:\n"
            f"  curl -s -X POST {base}/openai/config/update "
            '-H "Content-Type: application/json" -H "Authorization: Bearer $OWUI_ADMIN_TOKEN" '
            '-d \'{"ENABLE_OPENAI_API":true,"OPENAI_API_BASE_URLS":["' + upstream_url + '"],'
            '"OPENAI_API_KEYS":["<the key this run minted>"],"OPENAI_API_CONFIGS":{"0":{"enable":true}}}\'\n'
            "  (the recipe above REPLACES OWUI's whole connection list -- fine for a "
            "single-connection demo box, but check GET " + base + "/openai/config first "
            "if other connections might exist)",
            file=sys.stderr,
        )

    # No hardcoded credential defaults. Local/demo test account already
    # documented in scripts/seed-demo-owner.py's header comment
    # (asdas@asdas.sda / asdas) if a caller wants to set these explicitly.
    if not token and not (email and password):
        print(
            "owui config sync skipped: set OWUI_ADMIN_TOKEN (preferred) or "
            "OWUI_ADMIN_EMAIL/OWUI_ADMIN_PASSWORD to enable it",
            file=sys.stderr,
        )
        return None
    try:
        if not token:
            # Password sign-in only works on an Open WebUI that has a local
            # account; an OAuth-only instance has none, hence OWUI_ADMIN_TOKEN.
            status, body = owui_request(
                base, {"Content-Type": "application/json"}, "POST",
                "/api/v1/auths/signin", {"email": email, "password": password},
            )
            token = body.get("token") if isinstance(body, dict) else None
            if status != 200 or not token:
                warn(f"signin failed: {status} {body}")
                return False

        auth_headers = {"Content-Type": "application/json", "Authorization": f"Bearer {token}"}
        status, existing = owui_request(base, auth_headers, "GET", "/openai/config")
        if status != 200 or not isinstance(existing, dict):
            warn(f"config fetch failed: {status} {existing}")
            return False

        status, body = owui_request(
            base, auth_headers, "POST", "/openai/config/update",
            merge_owui_config(existing, upstream_url, raw_secret),
        )
        if status != 200:
            warn(f"config update failed: {status} {body}")
            return False
        print("owui config sync: ok", file=sys.stderr)
        return True
    except urllib.error.HTTPError as e:
        raw = e.read()
        warn(f"{e.code} {raw[:300]!r}")
        return False
    except (urllib.error.URLError, OSError, json.JSONDecodeError) as e:
        warn(str(e))
        return False


def rewrite_env_file(path: str, raw_secret: str) -> bool:
    """Replace (or append) the OWUI_SHIM_KEY assignment in a compose .env.

    Written to a temp file in the same directory and moved into place, so a
    crash or a full disk can never leave the deployment with a truncated
    .env. The original file mode is preserved because this file holds every
    other secret the stack boots with. Returns True on success."""
    try:
        with open(path, "r", encoding="utf-8") as fh:
            lines = fh.read().splitlines(keepends=True)
    except OSError as e:
        print(f"env file update FAILED: cannot read {path}: {e}", file=sys.stderr)
        return False

    assignment = f"{SHIM_ENV_VAR}={raw_secret}\n"
    replaced = False
    out = []
    for line in lines:
        if line.split("=", 1)[0].strip() == SHIM_ENV_VAR and not line.lstrip().startswith("#"):
            # Only the first assignment wins in docker compose --env-file, but
            # replace every one so a stale duplicate cannot shadow it later.
            out.append(assignment)
            replaced = True
        else:
            out.append(line)
    if not replaced:
        if out and not out[-1].endswith("\n"):
            out.append("\n")
        out.append(assignment)

    directory = os.path.dirname(os.path.abspath(path)) or "."
    try:
        mode = stat.S_IMODE(os.stat(path).st_mode)
        fd, tmp_path = tempfile.mkstemp(dir=directory, prefix=".env.shimkey.")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as fh:
                fh.write("".join(out))
            os.chmod(tmp_path, mode)
            os.replace(tmp_path, path)
        except OSError:
            os.unlink(tmp_path)
            raise
    except OSError as e:
        print(f"env file update FAILED: cannot write {path}: {e}", file=sys.stderr)
        return False
    print(
        f"env file update: ok ({SHIM_ENV_VAR} {'replaced' if replaced else 'appended'} in {path})",
        file=sys.stderr,
    )
    return True


def provision_billing_mapping(rest, headers, tenant_id: str, account_id: str) -> None:
    """Map tenant_id to account_id in public.tenant_billing_accounts.

    That row is what apps/control-plane/internal/apikeys/repository.go resolves
    an API key's tenant from. Without it edge-api answers 403
    account_not_provisioned to this shim key on GET /v1/models, on document RAG
    embeddings and on text-to-speech, even though the key itself is active and
    allowed every model, which is issue #717: the whole model picker came up
    empty because this script minted a key on an account no tenant billed.

    Shape follows the live signup path (signup.EnsureTenantBillingAccount): one
    row, tenant_id plus account_id, no ids invented here. The pairing is
    asserted rather than derived, because that path's convergence predicate
    cannot resolve this fixture: the bootstrap member below deliberately has no
    billing account of its own, which reads as "not converged yet" forever.

    Never moves an existing mapping. The table is 1:1 in both directions
    (tenant_id is the primary key, account_id is UNIQUE, so an account funds at
    most one tenant and a credit balance is never double-spent across two).
    Repointing either side is an operator decision about whose credits pay for
    whose traffic, so a collision exits non-zero here, before any key is minted
    or revoked, and names the fix. Two shim accounts cannot share one tenant:
    that is what --tenant-slug is for."""
    status, body = request(
        rest, headers, "GET", "/tenant_billing_accounts",
        params={"tenant_id": f"eq.{tenant_id}", "select": "account_id"},
    )
    if status != 200 or body is None:
        print(f"error: billing mapping lookup by tenant failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    if body:
        mapped_account = body[0]["account_id"]
        if mapped_account == account_id:
            print("billing mapping: ok (already mapped)", file=sys.stderr)
            return
        print(
            f"error: tenant {tenant_id} already bills to account {mapped_account}, not the shim "
            f"account {account_id}. One tenant bills to exactly one account, so the key this run "
            "would mint could never resolve a tenant and would 403 every model listing. Either "
            "run with --account-slug set to the account that mapping already names, or give this "
            "shim its own tenant with --tenant-slug.",
            file=sys.stderr,
        )
        sys.exit(1)

    status, body = request(
        rest, headers, "GET", "/tenant_billing_accounts",
        params={"account_id": f"eq.{account_id}", "select": "tenant_id"},
    )
    if status != 200 or body is None:
        print(f"error: billing mapping lookup by account failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    if body:
        print(
            f"error: shim account {account_id} already funds tenant {body[0]['tenant_id']}, not "
            f"{tenant_id}. An account funds at most one tenant. Either point this run at that "
            "tenant with --tenant-slug, or mint on a different account with --account-slug.",
            file=sys.stderr,
        )
        sys.exit(1)

    status, body = request(
        rest, headers, "POST", "/tenant_billing_accounts",
        body={"tenant_id": tenant_id, "account_id": account_id},
    )
    if status in (200, 201, 204):
        print("billing mapping: ok (created)", file=sys.stderr)
        return

    # A concurrent run can pass both lookups above and insert first, so this
    # insert can lose on either uniqueness constraint to a row that already
    # says exactly what this run wanted. Re-read before failing: the same pair
    # is a success, any other pair is the collision the messages above
    # describe. Without this, two overlapping runs make one of them fail for no
    # reason anybody has to act on.
    reread_status, rows = request(
        rest, headers, "GET", "/tenant_billing_accounts",
        params={"tenant_id": f"eq.{tenant_id}", "select": "account_id"},
    )
    if reread_status == 200 and rows and rows[0]["account_id"] == account_id:
        print("billing mapping: ok (a concurrent run created the same pairing first)", file=sys.stderr)
        return
    print(f"error: billing mapping insert failed: {status} {body}", file=sys.stderr)
    sys.exit(1)


def provision_tenant_member(
    rest, gotrue, headers, tenant_id: str, email: str, role: str, env_password: str = "",
) -> tuple[str, str | None]:
    """Find-or-create a GoTrue user for `email` and upsert its `role`
    membership in `tenant_id`. Returns (user_id, password), where password is
    None when this run deliberately left an existing account's password
    untouched: see password_to_set. Shared by the real fixture user and the
    throwaway bootstrap user (see module docstring)."""
    # `filter=<email>` does an exact server-side match (verified live; the
    # `email=` param is NOT supported and 500s on this GoTrue version).
    status, body = request(gotrue, headers, "GET", "/admin/users", params={"filter": email})
    if status != 200:
        print(f"error: user lookup failed for {email}: {status} {body}", file=sys.stderr)
        sys.exit(1)
    existing = next(
        (u for u in body.get("users", []) if u.get("email", "").lower() == email.lower()),
        None,
    )

    password = password_to_set(existing is not None, env_password, random_password())
    user_metadata = {"selected_tenant_id": tenant_id}

    if existing is None:
        status, body = request(
            gotrue, headers, "POST", "/admin/users",
            body={
                "email": email,
                "password": password,
                "email_confirm": True,
                "user_metadata": user_metadata,
            },
        )
        if status not in (200, 201):
            print(f"error: user create failed for {email}: {status} {body}", file=sys.stderr)
            sys.exit(1)
        user_id = body["id"]
    else:
        user_id = existing["id"]
        # GoTrue's admin updateUserById MERGES user_metadata (verified
        # live), so this only ever adds/refreshes selected_tenant_id.
        # The password key is present only when this run is entitled to write
        # it (see password_to_set): omitting it leaves every session another
        # run is holding on this account alive.
        update = {"user_metadata": user_metadata}
        if password is not None:
            update["password"] = password
        status, body = request(
            gotrue, headers, "PUT", f"/admin/users/{user_id}", body=update,
        )
        if status != 200:
            print(f"error: user update failed for {email}: {status} {body}", file=sys.stderr)
            sys.exit(1)

    status, body = request(
        rest, headers, "POST", "/tenant_users",
        body={
            "tenant_id": tenant_id,
            "user_id": user_id,
            "role": role,
            "status": MEMBER_STATUS,
        },
        params={"on_conflict": "tenant_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: membership upsert failed for {email}: {status} {body}", file=sys.stderr)
        sys.exit(1)
    return user_id, password


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument(
        "--account-slug",
        default=os.environ.get("OWUI_SHIM_ACCOUNT_SLUG", "").strip() or SHIM_ACCOUNT_SLUG,
        help=(
            "Billing account the shim key is minted on. Defaults to the CI "
            f"account ({SHIM_ACCOUNT_SLUG}), which the nightly OWUI job rotates "
            "on a schedule. A long-lived deployment must pass its own slug so a "
            "scheduled run cannot revoke the key it depends on."
        ),
    )
    parser.add_argument(
        "--tenant-slug",
        default=os.environ.get("OWUI_TENANT_SLUG", "").strip() or TENANT_SLUG,
        help=(
            "Tenant the shim account bills to, and the tenant the fixture users "
            f"are members of. Defaults to CI's ({TENANT_SLUG}). One tenant bills "
            "to exactly one account, so a long-lived deployment that passes its "
            "own --account-slug must pass its own --tenant-slug too, or the two "
            "shim accounts collide on this tenant and whichever runs second "
            "cannot be provisioned at all."
        ),
    )
    parser.add_argument(
        "--env-file",
        default="",
        help=(
            f"Path to a compose .env whose {SHIM_ENV_VAR} should be rewritten to "
            "the newly minted key, before the previous key is revoked."
        ),
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
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
    tenant_slug = args.tenant_slug
    tenant_name = TENANT_NAME if tenant_slug == TENANT_SLUG else f"OWUI E2E ({tenant_slug})"
    status, body = request(
        rest, headers, "POST", "/tenants",
        body={"slug": tenant_slug, "name": tenant_name, "deployment": TENANT_DEPLOYMENT},
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: tenant upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    tenant_id = body[0]["id"]

    # 2 + 3. Provision the bootstrap user first so it -- not the real fixture
    # user below -- absorbs Open WebUI's unconditional first-user-becomes-
    # admin promotion (see module docstring).
    run_key = os.environ.get("OWUI_E2E_RUN_KEY", "")
    user_email = with_run_key(USER_EMAIL, run_key)
    bootstrap_email = with_run_key(BOOTSTRAP_EMAIL, run_key)

    _, bootstrap_password = provision_tenant_member(
        rest, gotrue, headers, tenant_id, bootstrap_email, BOOTSTRAP_ROLE,
        os.environ.get("OWUI_E2E_BOOTSTRAP_PASSWORD", ""),
    )

    user_id, password = provision_tenant_member(
        rest, gotrue, headers, tenant_id, user_email, USER_ROLE,
        os.environ.get("OWUI_E2E_PASSWORD", ""),
    )

    # 4. Upsert the throwaway shim billing account (older accounts/api_keys
    # schema -- separate from the tenants/tenant_users rows above).
    account_slug = args.account_slug
    account_name = SHIM_ACCOUNT_NAME if account_slug == SHIM_ACCOUNT_SLUG else f"OWUI Shim ({account_slug})"
    status, body = request(
        rest, headers, "POST", "/accounts",
        body={
            "slug": account_slug,
            "display_name": account_name,
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

    # 4b. Map the tenant to that account. Runs BEFORE the mint below so a
    # collision it cannot resolve costs nothing: no key is minted and none is
    # revoked, and the deployment keeps working on whatever it already had.
    # Without this row every key minted here 403s account_not_provisioned on
    # model listing, RAG embeddings and TTS (issue #717).
    provision_billing_mapping(rest, headers, tenant_id, shim_account_id)

    # 5. Mint the new shim key FIRST, and revoke the account's previous keys
    # only in step 7, after every configured consumer carries the new value.
    # The old order (delete, mint, then best-effort consumer sync) left a
    # deployment on a revoked key whenever the sync failed.
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

    # 6. Update every configured consumer of this value: the compose .env a
    # restart reads, and Open WebUI's own persisted OpenAI config (which
    # outlives the env var, see module docstring). Neither is configured in
    # CI, which materializes a fresh .env.ci and a fresh OWUI container per
    # run; both are the whole point on a long-lived deployment.
    consumer_failed = False
    if args.env_file:
        consumer_failed = not rewrite_env_file(args.env_file, raw_secret) or consumer_failed
    consumer_failed = sync_owui_config(raw_secret) is False or consumer_failed

    # 7. Revoke the account's STALE keys (cascades to their policy rows). Held
    # back until the consumers above are updated: revoking first is what turns
    # a failed sync into an outage.
    #
    # The billing account is shared even when the users are not: OWUI_E2E_RUN_KEY
    # namespaces the GoTrue identities, and deliberately does not namespace this
    # account, because a tenant bills to exactly one account and inventing a
    # tenant per run would leave a permanent row behind on every run. So the
    # deletion is bounded by age rather than by "everything except mine". Two
    # runs overlapping (the schedule and a labelled pull request sit in
    # different concurrency groups, so they can) used to revoke each other's key
    # mid-flight, which is the outage already recorded in .wolf/cerebrum.md.
    # A key minted minutes ago now survives any concurrent run.
    if consumer_failed:
        print(
            "error: leaving the previous shim key(s) active because a configured "
            "consumer was not updated; the deployment keeps working on the old key. "
            "Fix the failure above and re-run.",
            file=sys.stderr,
        )
    else:
        cutoff = stale_key_cutoff_iso()
        status, body = request(
            rest, headers, "DELETE", "/api_keys",
            params={
                "account_id": f"eq.{shim_account_id}",
                "id": f"neq.{shim_key_id}",
                "created_at": f"lt.{cutoff}",
            },
        )
        if status not in (200, 204):
            print(f"error: shim key cleanup failed: {status} {body}", file=sys.stderr)
            sys.exit(1)

    # 8. Remove the run-scoped users older runs left behind. After the key
    # revocation above, so a stale user no longer has an api_keys row pointing
    # at it. Best effort: never fails the run.
    sweep_stale_fixture_users(gotrue, headers, run_key)

    # A PASSWORD line appears only when this run actually set that password.
    # For an account that already existed, this run does not know the password
    # and must not invent one, so the line is omitted and the reason is named
    # on stderr. A caller that needs the line (the nightly workflow does, and
    # fails loudly without it) either supplies the value or asks for a
    # run-scoped identity that has no prior password to preserve.
    print(f"EMAIL={user_email}")
    if password is not None:
        print(f"PASSWORD={password}")
    print(f"SHIM_KEY={raw_secret}")
    print(f"BOOTSTRAP_EMAIL={bootstrap_email}")
    if bootstrap_password is not None:
        print(f"BOOTSTRAP_PASSWORD={bootstrap_password}")

    for label, value, variable in (
        ("fixture user", password, "OWUI_E2E_PASSWORD"),
        ("bootstrap user", bootstrap_password, "OWUI_E2E_BOOTSTRAP_PASSWORD"),
    ):
        if value is None:
            print(
                f"note: the {label} already existed, so its password was left "
                f"untouched and no line was printed for it. Rotating a shared "
                f"account revokes every session another run is holding on it "
                f"(docs/live-test-auth.md). Set OWUI_E2E_RUN_KEY to provision a "
                f"run-scoped identity instead, or {variable} to write a password "
                f"deliberately.",
                file=sys.stderr,
            )

    if consumer_failed:
        sys.exit(1)


if __name__ == "__main__":
    main()
