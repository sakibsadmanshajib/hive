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
see shared_billing_mapping.provision_billing_mapping.

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
That is enforced rather than merely documented (issue #560): see
assert_account_scope, which refuses both a run on CI's rotated account
that configures a long-lived consumer and a run on any other account that
configures none, before anything is minted or revoked. The cleanup is
also filtered to the account's own key nickname (key_nickname, derived
from the slug), so a key minted by any other route and carried by
who-knows-what is never revoked here. And when --env-file is the
consumer, the revocation additionally requires that the file was carrying
one of this account's keys before this run rewrote it, so a run pointed
at a scratch path or at the wrong deployment's .env revokes nothing.
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

# scripts/ is sys.path[0] for `python3 scripts/<name>.py`, so this plain
# import needs no packaging. See the module docstring for why the tenant to
# account mapping is shared with seed-demo-owner.py rather than copied.
import shared_billing_mapping

TENANT_SLUG = "owui-e2e"
TENANT_NAME = "OWUI E2E"
# Default account is CI's. The nightly OWUI job rotates it on a schedule, so a
# long-lived deployment must pass its own --account-slug (see module docstring).
SHIM_ACCOUNT_SLUG = "owui-e2e-shim"
SHIM_ACCOUNT_NAME = "OWUI E2E Shim"
SHIM_KEY_NICKNAME = "owui-e2e-shim-key"
SHIM_ENV_VAR = "OWUI_SHIM_KEY"


def is_ci_account(account_slug: str) -> bool:
    """Whether this slug names the reserved account the nightly OWUI job rotates.

    Case-insensitive on purpose. public.accounts.slug is `text not null unique`
    and case sensitive (supabase/migrations/20260328_01_identity_foundation.sql),
    so `--account-slug OWUI-E2E-SHIM` would otherwise miss the reserved arm of
    assert_account_scope, be treated as a deployment account, and upsert a
    near-duplicate row that nothing rotates. Only the comparison is folded; the
    slug sent to PostgREST is still exactly what the caller passed."""
    return account_slug.casefold() == SHIM_ACCOUNT_SLUG


def key_nickname(account_slug: str) -> str:
    """Nickname this script mints under, and the only nickname it will revoke.

    Derived from the account rather than fixed, because a constant made the
    boundary a coincidence rather than a property. The demo box's live key is
    nicknamed `hive-demo-owui-shim-key` on account `hive-demo-owui-shim`, which
    is why the constant spared it: it was minted by hand and simply did not
    match. The first operator to follow the documented remedy would have minted
    over it with SHIM_KEY_NICKNAME and destroyed that protection for good.

    Derived, three things hold by construction instead of by history. CI's
    cleanup can never match a deployment's key even if the two ever share an
    account, because the two nicknames cannot collide. The documented remedy
    reproduces `hive-demo-owui-shim-key`, so recovery preserves the box's
    existing name rather than replacing it. And the remedy run then revokes the
    key it just replaced, instead of leaving a superseded credential active
    forever.

    A key minted under any other name (by hand, by another tool) is still never
    revoked here: this run has no idea who carries it. It stays active until an
    operator revokes it deliberately, which .env.example and the
    OWUIShimKeyUnusable alert both now say out loud."""
    return SHIM_KEY_NICKNAME if is_ci_account(account_slug) else f"{account_slug}-key"
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
    if not owui_sync_configured():
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


def read_env_assignment(path: str) -> str:
    """Value of the first uncommented OWUI_SHIM_KEY assignment in a .env, or "".

    Read before the rewrite below replaces it, because it is the only evidence
    this run has about WHICH deployment the --env-file it was handed belongs
    to. Returns "" for a missing file, a missing assignment or an empty one:
    every one of those means this file was not carrying a key, which the caller
    treats as "there is nothing here whose revocation this run has earned"."""
    try:
        with open(path, "r", encoding="utf-8") as fh:
            lines = fh.read().splitlines()
    except OSError:
        return ""
    for line in lines:
        if line.lstrip().startswith("#"):
            continue
        name, sep, value = line.partition("=")
        if sep and name.strip() == SHIM_ENV_VAR:
            return value.strip()
    return ""


def env_file_carried_this_accounts_key(rest, headers, account_id: str, previous_secret: str) -> bool:
    """Whether the value --env-file held BEFORE this run is a key on this account.

    --env-file proves that a file was rewritten, not that the file belongs to
    the deployment carrying the key this run is about to revoke. `--env-file
    /tmp/scratch.env`, or another deployment's .env, satisfies the scope guard
    and the consumer-sync check just as well as the right one, and then the
    revocation in step 7 takes the real holder down: the outage in issue #560,
    reached through the arm the guard allows.

    So the invariant this script states in prose gets a machine check. A key a
    long-lived consumer carries may only ever be revoked by the run that just
    replaced it in that consumer, and the proof that this run replaced it is
    that the value it overwrote hashes to a key row on this very account. The
    hash is the same sha256 of the raw secret that random_api_key computes and
    api_keys.token_hash stores, so no secret is sent anywhere.

    False on first-time setup (.env ships an empty OWUI_SHIM_KEY=, so there is
    no previous key and nothing to revoke) and false on a wrong file, which are
    the same answer for the same reason."""
    if not previous_secret:
        return False
    digest = hashlib.sha256(previous_secret.encode()).hexdigest()
    status, body = request(
        rest, headers, "GET", "/api_keys",
        params={
            "account_id": f"eq.{account_id}",
            "token_hash": f"eq.{digest}",
            "select": "id",
            "limit": "1",
        },
    )
    return status == 200 and bool(body)


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


# The knobs THIS script exposes for moving an identity off a collision, handed
# to the shared guard so its exit message names a flag that exists here. The
# mapping itself lives in shared_billing_mapping.py: both seeders write that
# row, and the two copies of the rule drifted once already (this script learned
# it in issue #717, seed-demo-owner.py had not, which is issue #1599).
BILLING_MAPPING_OPTIONS = ("--tenant-slug", "--account-slug")


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


def owui_sync_configured() -> bool:
    """True when this run can push a newly minted key into Open WebUI's own
    persisted config, which is one of the two long-lived consumers of the
    value. Read by sync_owui_config below and by assert_account_scope, from
    one place, so the guard and the sync can never disagree about whether a
    consumer exists."""
    token = os.environ.get("OWUI_ADMIN_TOKEN", "").strip()
    email = os.environ.get("OWUI_ADMIN_EMAIL", "").strip()
    password = os.environ.get("OWUI_ADMIN_PASSWORD", "").strip()
    return bool(token or (email and password))


def assert_account_scope(account_slug: str, env_file: str) -> None:
    """Refuse the two configurations in which this script could revoke a
    credential a long-lived deployment is still carrying (issue #560).

    The account boundary was already the mechanism, but only as prose: the
    docstring said a deployment must pass its own --account-slug, and nothing
    enforced it. So the failure it describes stayed reachable in both
    directions, and it is not hypothetical, it is what happened. Both are
    refused here, before a network call is made, so no key is minted and none
    is revoked on a configuration this script will not stand behind.

    The invariant, stated once: a key that a long-lived consumer carries may
    only ever be revoked by the run that just replaced it in that consumer.

      * The reserved CI account is rotated on a schedule by a run that
        configures no consumer and knows about none. Handing it a consumer is
        how a deployment ends up on the rotated account, so it is refused.
      * Any other account is a deployment's. A run that updates no consumer
        there would revoke keys nobody told the deployment to stop using, so
        it is refused too.

    Exit code 2, not 1, so a caller can tell a refused configuration from a
    failed operation."""
    has_consumer = bool(env_file.strip()) or owui_sync_configured()
    if not account_slug:
        # An empty slug misses the reserved comparison below, so with any
        # consumer configured it would pass the guard, upsert an account whose
        # slug is the empty string, and hand the consumer a credential on a
        # garbage account that nothing rotates and nobody owns.
        print(
            "error: --account-slug is empty. Pass the reserved CI account "
            f"({SHIM_ACCOUNT_SLUG}) or this deployment's own slug; an empty "
            "slug would mint a key on an account nothing rotates and nobody owns.",
            file=sys.stderr,
        )
        sys.exit(2)
    if is_ci_account(account_slug) and has_consumer:
        print(
            f"error: refusing to point a long-lived consumer at {SHIM_ACCOUNT_SLUG}, "
            "the account the nightly OWUI job rotates on a schedule. That run "
            "revokes this account's keys and cannot update your deployment, so "
            "document RAG and text-to-speech would die there with no signal "
            "(issue #560). Pass --account-slug <your-deployment> (and its own "
            "--tenant-slug) instead, or drop --env-file and the OWUI_ADMIN_* "
            "variables if you meant to rotate CI's key.",
            file=sys.stderr,
        )
        sys.exit(2)
    if not is_ci_account(account_slug) and not has_consumer:
        print(
            f"error: refusing to rotate {account_slug}, a deployment account, "
            "without updating anything that carries the key. Revoking here and "
            "syncing nothing is exactly the outage in issue #560. Pass "
            "--env-file <path to that deployment's compose .env>, and set "
            "OWUI_BASE_URL with OWUI_ADMIN_TOKEN so Open WebUI's persisted "
            "config is updated too.",
            file=sys.stderr,
        )
        sys.exit(2)


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
    args = parser.parse_args()
    # Normalised once, here, so the scope guard below and the account upsert
    # further down can never be comparing different strings. A padded
    # " owui-e2e-shim" would otherwise miss the reserved-slug comparison AND
    # create a second, near-identical account row that nothing rotates.
    args.account_slug = args.account_slug.strip()
    args.tenant_slug = args.tenant_slug.strip()
    args.env_file = args.env_file.strip()
    return args


def main() -> None:
    args = parse_args()
    # Before any credential is read and before the first request: a refused
    # configuration must cost nothing, and in particular must not mint a key
    # it then declines to finish wiring up.
    assert_account_scope(args.account_slug, args.env_file)
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
    account_name = SHIM_ACCOUNT_NAME if is_ci_account(account_slug) else f"OWUI Shim ({account_slug})"
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
    shared_billing_mapping.provision_billing_mapping(
        request, rest, headers, tenant_id, shim_account_id, BILLING_MAPPING_OPTIONS,
    )

    # 5. Mint the new shim key FIRST, and revoke the account's previous keys
    # only in step 7, after every configured consumer carries the new value.
    # The old order (delete, mint, then best-effort consumer sync) left a
    # deployment on a revoked key whenever the sync failed.
    raw_secret, token_hash, redacted_suffix = random_api_key()
    status, body = request(
        rest, headers, "POST", "/api_keys",
        body={
            "account_id": shim_account_id,
            "nickname": key_nickname(account_slug),
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
    previous_env_secret = ""
    if args.env_file:
        # Before the rewrite, not after: the value this file held is the only
        # evidence that it belongs to the deployment whose keys step 7 revokes.
        previous_env_secret = read_env_assignment(args.env_file)
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
    # An --env-file this run rewrote but that was not carrying a key on this
    # account is not proof of anything: the file may be a scratch path or
    # another deployment's .env, and revoking here takes down whoever really
    # holds this account's key. Checked only when --env-file is the consumer;
    # the OWUI arm talks to a live Open WebUI, which is identity enough.
    env_file_unproven = bool(args.env_file) and not env_file_carried_this_accounts_key(
        rest, headers, shim_account_id, previous_env_secret,
    )
    if consumer_failed:
        print(
            "error: leaving the previous shim key(s) active because a configured "
            "consumer was not updated; the deployment keeps working on the old key. "
            "Fix the failure above and re-run.",
            file=sys.stderr,
        )
    elif env_file_unproven:
        print(
            f"note: leaving this account's older keys active. {args.env_file} was not "
            f"carrying one of this account's keys before this run rewrote it, so this "
            "run has not replaced anything there and has not earned the right to "
            "revoke anything (issue #560). That is normal on first-time setup, where "
            f"{SHIM_ENV_VAR} is empty and there is nothing to revoke. Otherwise the "
            "path is the wrong deployment's .env: check it, and note that the new key "
            "has already been written into it.",
            file=sys.stderr,
        )
    else:
        cutoff = stale_key_cutoff_iso()
        status, body = request(
            rest, headers, "DELETE", "/api_keys",
            params={
                "account_id": f"eq.{shim_account_id}",
                # Only keys minted under THIS account's nickname (see
                # key_nickname: derived from the slug, so CI's cleanup and a
                # deployment's can never name the same key even if the two ever
                # shared an account). A key put on this account under any other
                # name is left alone: this run has no idea who carries it, and
                # revoking a credential nobody told the holder about is the
                # whole of issue #560. It stays active until an operator revokes
                # it deliberately, which .env.example and the alert both now
                # tell them to do rather than leaving it to be discovered.
                "nickname": f"eq.{key_nickname(account_slug)}",
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
