#!/usr/bin/env python3
"""Idempotently provision the cross-surface Hive demo account.

Solves the "3 auth systems, 0 shared admin account" gap for live demos: one
Supabase GoTrue user that is simultaneously an OWNER of a real (non-e2e)
tenant (unlocks agent-console's Cowork task console, gated on ENABLE_COWORK
via apps/agent-console/lib/edge-api/gate.ts) and an owner of a web-console
personal account (unlocks the owner-only billing pages). Since issue #758 the
workspace-scoped admin panels -- feature gates and the marketplace -- are
reached through that same tenant OWNER role, by apps/control-plane/internal/
platform.WorkspaceAdminGate, see internal/platform/http/router.go.

This script deliberately does NOT grant platform admin. accounts.
is_platform_admin is written false, and because the account upsert runs with
resolution=merge-duplicates, re-running the seeder clears that flag on an
existing demo account rather than merely declining to set it. The platform-
wide powers behind RoleService.RequirePlatformAdmin -- credit minting,
provider base-URL rewrites, catalog curation -- stay a deployment-operator
concern, and a demo account must not hold them.

Two independent role systems get written here, on purpose, same account:
  - tenant_users.role = 'OWNER'   (Phase 19 tenant scope; uppercase enum)
  - account_memberships.role = 'owner', accounts.is_platform_admin = false
    (Phase 2 billing-account scope; lowercase enum, separate schema)
web-console reads the second system (lib/control-plane/client.ts getViewer);
agent-console's tenant gate and the custom_access_token_hook JWT claims read
the first. Neither table know about the other -- see
apps/control-plane/internal/platform/role.go's Phase 14/18 module comment.

OWUI is NOT covered by this script. docker-compose.yml wires OWUI's OIDC
("Sign in with Hive") against SUPABASE_URL, but SUPABASE_OAUTH_CLIENT_ID /
SUPABASE_OAUTH_CLIENT_SECRET are unset in .env, so the button is inert on
this stack, and open-webui's OAUTH_ALLOWED_ROLES ("ADMIN,MEMBER,VIEWER")
does not even include the OWNER role this script grants -- OWNER would fail
that allow-list if OIDC were wired. OWUI identity stays separate; use the
existing local OWUI test account (asdas@asdas.sda / asdas) for the chat
surface demo.

Required env: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY
Optional env: HIVE_DEMO_PASSWORD -- set the account's password to this value.
Optional env: HIVE_DEMO_CREDITS -- grant this many credits to the account, as
one append-only public.credit_ledger_entries row of entry_type 'grant' whose
metadata states why it exists. There is no default and no implicit grant:
unset means the workspace is provisioned with a zero balance, because credit
on Hive is owner-discretionary (no trial, no signup bonus, no referral
reward). Unit is credits, not dollars; 1 USD = 1,000,000,000 credits.

  AT MOST ONE GRANT PER ACCOUNT, EVER. The idempotency key carries the
  account and not the amount (grant_idempotency_key), so a later run with a
  different amount is refused by the ledger's unique index rather than added
  to the first. This run says so on stderr, naming what the account already
  holds, instead of reporting a silent no-op. That is deliberate: this script
  is idempotent by contract and re-running it is the documented recovery
  procedure, so a version that stacked grants would move real money by an
  unbounded amount under correct-looking operator behaviour. Changing the
  funding afterwards belongs on the platform admin credit grants surface,
  which records who granted it and why. See grant_ledger_row for the
  deliberate deviation from grants.CreateWithLedger and what a seeded grant
  therefore does not carry.
Optional env (identity, see env_or): HIVE_DEMO_EMAIL, HIVE_DEMO_TENANT_SLUG,
HIVE_DEMO_TENANT_NAME, HIVE_DEMO_ACCOUNT_SLUG, HIVE_DEMO_ACCOUNT_NAME. Set
all three of email/tenant-slug/account-slug together to provision a second,
independent owner alongside the default demo one. Setting only some of the
three is refused by validate_identity_overrides below: env_or falls back to
the default per variable independently, so a partial override would silently
attach the new tenant/account slugs to the shared default demo user instead
of the separate identity the operator meant to create.

Prints to stdout (and nothing else):
  EMAIL=<email>
  PASSWORD=<password>   only when this run actually set a password

Both lines are printed as soon as the identity exists, before the tenant,
account and billing rows are provisioned, so a run that fails partway still
hands its caller the credential it just minted. Branch on the exit status, not
on the presence of these lines, to decide whether the workspace is complete.

A PASSWORD line appears when the account was created by this run, or when
HIVE_DEMO_PASSWORD contains a non-whitespace value. For an account that already
exists with no such value, the password is left untouched and no PASSWORD line
is printed, because this run does not know it. See password_to_set for why the
default is no longer to rotate. Callers that need a credential must either pass
HIVE_DEMO_PASSWORD or already hold one; a caller that reads PASSWORD
unconditionally will see the line disappear rather than receive a stale value.

Everything else (progress, ids, errors) goes to stderr.
"""
import json
import os
import secrets
import string
import sys
import urllib.error
import urllib.parse
import urllib.request

# `scripts/` is sys.path[0] for `python3 scripts/<name>.py`, including the
# relative invocation in demo-chat-settings-check.yml, so this plain import
# needs no packaging. See the module's own docstring for why the tenant to
# account mapping is shared with seed-owui-e2e-user.py rather than copied.
import shared_billing_mapping


def env_or(name: str, default: str) -> str:
    """An optional env override for one of the identity constants below.

    The identity was hardcoded while this script provisioned exactly one
    account. A deployment needs more than one: the shared demo login that
    several agents hold sessions against, and a separate named owner login for
    a real person. Those cannot be the same row -- the guards below (correctly)
    refuse to merge a second human onto the demo tenant, and rotating the
    demo password to hand it over revokes every live session on it.

    So slug and email are parameters, not constants. Defaults are the original
    values, so a call with no overrides set provisions exactly what it always
    did. Whitespace-only is treated as unset, matching env() below.
    """
    return os.environ.get(name, "").strip() or default


IDENTITY_OVERRIDE_VARS = ("HIVE_DEMO_EMAIL", "HIVE_DEMO_TENANT_SLUG", "HIVE_DEMO_ACCOUNT_SLUG")


def validate_identity_overrides(environ) -> None:
    """Fail fast unless the three identity variables are set together or not
    at all.

    env_or defaults each variable independently, so setting only the slugs
    (or only the email) does not raise a second identity -- it silently
    attaches the new tenant/account slugs to the SHARED default demo user,
    because HIVE_DEMO_EMAIL fell back to its default. That defeats the only
    reason to override the slugs in the first place, with no error and no
    warning. Whitespace-only counts as unset, matching env_or's own
    stripping.
    """
    set_vars = [name for name in IDENTITY_OVERRIDE_VARS if environ.get(name, "").strip()]
    if set_vars and len(set_vars) != len(IDENTITY_OVERRIDE_VARS):
        missing_vars = [name for name in IDENTITY_OVERRIDE_VARS if name not in set_vars]
        print(
            "error: HIVE_DEMO_EMAIL, HIVE_DEMO_TENANT_SLUG, and HIVE_DEMO_ACCOUNT_SLUG "
            "must be set together (to provision a second, independent owner) or left "
            f"unset together (for the shared default demo identity) -- set: {', '.join(set_vars)}; "
            f"missing: {', '.join(missing_vars)}.",
            file=sys.stderr,
        )
        sys.exit(1)


validate_identity_overrides(os.environ)

TENANT_SLUG = env_or("HIVE_DEMO_TENANT_SLUG", "hive-demo")
TENANT_NAME = env_or("HIVE_DEMO_TENANT_NAME", "Hive Demo")
TENANT_DEPLOYMENT = "HIVE_CLOUD"
# .invalid: IANA-reserved TLD (RFC 2606). A real deliverable address is fine
# too -- the account is created with email_confirm=true, so no mail is sent.
USER_EMAIL = env_or("HIVE_DEMO_EMAIL", "demo@hive-demo.invalid")
TENANT_ROLE = "OWNER"
TENANT_STATUS = "ACTIVE"

ACCOUNT_SLUG = env_or("HIVE_DEMO_ACCOUNT_SLUG", "hive-demo-owner")
ACCOUNT_NAME = env_or("HIVE_DEMO_ACCOUNT_NAME", "Hive Demo")

# Demo-relevant tenant_settings feature gates. Excludes payment-rail toggles
# (bkash/sslcommerz/stripe), audit sinks, and SSO -- none of those are on the
# demo path and leaving them off keeps the tenant's gate surface legible.
FEATURE_GATES = [
    "ENABLE_ADMIN_CONSOLE",
    "ENABLE_PROVIDER_CUSTOM",
    "ENABLE_RAG",
    "ENABLE_RAG_PERSONAL",
    "ENABLE_RAG_SHARED_KB",
    "ENABLE_VOICE",
    "ENABLE_RELAY",
    "ENABLE_COWORK",
]


def env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        print(f"error: {name} is not set", file=sys.stderr)
        sys.exit(1)
    return value


REQUEST_TIMEOUT_SECONDS = 15

# Every request this script sends carries an explicit User-Agent, and that is
# load bearing rather than politeness (same fix as scripts/post-deploy-verify.py
# and scripts/preflight-supabase-config.py, same root cause): the demo hostnames
# sit behind Cloudflare, whose bot rules answer 403 "error code: 1010" to the
# literal default urllib sends (`Python-urllib/3.x`). Measured live against
# console-hive.scubed.co's /auth/v1/admin/users (deploy-demo-box.yml's
# agent-workspace-coverage job, run 33290707452, 2026-08-30), not guessed.
USER_AGENT = "hive-seed-demo-owner/1 (+https://github.com/sakibsadmanshajib/hive)"


def request(base, headers, method, path, body=None, params=None, prefer=None):
    url = base + path
    if params:
        url += "?" + urllib.parse.urlencode(params)
    data = json.dumps(body).encode() if body is not None else None
    req_headers = dict(headers)
    req_headers.setdefault("User-Agent", USER_AGENT)
    if prefer:
        req_headers["Prefer"] = prefer
    req = urllib.request.Request(url, data=data, method=method, headers=req_headers)
    try:
        with urllib.request.urlopen(req, timeout=REQUEST_TIMEOUT_SECONDS) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except json.JSONDecodeError:
            print(f"error: {method} {path} -> {e.code}: {raw[:300]!r}", file=sys.stderr)
            sys.exit(1)
    except urllib.error.URLError as e:
        # Without a timeout, a hung Supabase endpoint blocked the operator
        # indefinitely -- urlopen wraps a connect/read timeout as a URLError
        # whose reason is a TimeoutError, so surface that case with a clearer
        # message than the generic reason repr.
        if isinstance(e.reason, TimeoutError):
            print(
                f"error: {method} {path} -> timed out after {REQUEST_TIMEOUT_SECONDS}s; "
                "check SUPABASE_URL and network reachability",
                file=sys.stderr,
            )
        else:
            print(f"error: {method} {path} -> {e.reason}", file=sys.stderr)
        sys.exit(1)


def random_password() -> str:
    # Prefix guarantees upper/lower/digit/symbol classes regardless of the
    # random draw; total length (28) clears any realistic GoTrue min-length
    # policy with room to spare, well under bcrypt's 72-byte limit.
    alphabet = string.ascii_letters + string.digits + "!@#$%^&*-_"
    return "Aa1!" + "".join(secrets.choice(alphabet) for _ in range(24))


def password_to_set(user_exists: bool, env_password: str, new_user_password: str) -> str | None:
    """The password this run should write, or None to leave it untouched.

    This script used to rotate unconditionally so that no run reused a prior
    credential. That was correct while a single operator ran it. Several agents
    now share this account concurrently, and because the control-plane resolves
    every bearer against GoTrue on each request, a rotation revokes their live
    sessions mid-task. It happened repeatedly in one session.

    So the default is now to leave an existing account alone, and a caller that
    genuinely wants to set the password says so through HIVE_DEMO_PASSWORD.
    A brand-new account still gets new_user_password, since there is no session
    to break and no credential to preserve. Generation stays in the caller so
    this selector is pure and directly assertable.
    """
    env_password = env_password.strip()
    if env_password:
        return env_password
    if not user_exists:
        return new_user_password
    return None


# Credit unit, owner directive 2026-08-23:
# supabase/migrations/20260823_40_credit_unit_rescale_billion.sql. Kept here as
# a display and validation constant only; this script never converts an amount,
# it grants exactly the credits it was given.
CREDITS_PER_USD = 1_000_000_000

# public.credit_ledger_entries.credits_delta is a bigint.
MAX_BIGINT = 2**63 - 1


# The knobs THIS script exposes for moving an identity off a collision, handed
# to the shared guard so its exit message names a variable that exists here.
BILLING_MAPPING_OPTIONS = ("HIVE_DEMO_TENANT_SLUG", "HIVE_DEMO_ACCOUNT_SLUG")


def credits_to_grant(raw: str | None) -> int | None:
    """Credits this run should grant, or None to grant nothing.

    Credit on Hive is owner-discretionary: there is no trial, no signup bonus
    and no referral reward, and no code path grants credit implicitly. So the
    amount is passed in through HIVE_DEMO_CREDITS or nothing is granted at all,
    and there is deliberately no default -- an operator funding a demo
    workspace says how much, in writing, every time.

    Anything that is not a positive integer is refused loudly rather than
    rounded, truncated or quietly skipped: a typo in a money amount must not
    turn into a silent zero, and a fractional or exponent-notation amount has
    no meaning in a bigint ledger column. Unit is CREDITS, not dollars, matching
    public.credit_ledger_entries.credits_delta directly so nothing here does
    money arithmetic (1 USD = 1,000,000,000 credits since 2026-08-23; see
    supabase/migrations/20260823_40_credit_unit_rescale_billion.sql).
    """
    value = (raw or "").strip()
    if not value:
        return None
    # int() accepts leading signs and surrounding whitespace but not floats,
    # exponents or separators, which is exactly the acceptance surface wanted.
    # str.isdigit() first so "+5" and "5_0" are refused rather than accepted,
    # and isascii() with it because isdigit() is true for characters int()
    # itself rejects ("2" superscript, for one), which would raise instead of
    # printing the sentence below.
    if not (value.isascii() and value.isdigit()) or int(value) <= 0:
        print(
            f"error: HIVE_DEMO_CREDITS must be a positive integer number of credits, got "
            f"{value!r}. Leave it unset to grant nothing (credit on Hive is owner-discretionary; "
            f"1 USD = {CREDITS_PER_USD:,} credits).",
            file=sys.stderr,
        )
        sys.exit(1)
    if int(value) > MAX_BIGINT:
        # credits_delta is a bigint. Caught here rather than in Postgres, which
        # would refuse the write only after the whole workspace had already
        # been provisioned by the steps above.
        print(
            f"error: HIVE_DEMO_CREDITS is {value}, past the largest value the credit ledger can "
            f"hold ({MAX_BIGINT}). The unit is credits, not dollars: 1 USD = "
            f"{CREDITS_PER_USD:,} credits.",
            file=sys.stderr,
        )
        sys.exit(1)
    return int(value)


def format_usd_from_credits(credits: int) -> str:
    """The credit amount as dollars, for the confirmation line only.

    The unit an operator passes is credits, and somebody thinking in dollars is
    off by a billion in either direction, so the run says what it granted in
    both units and a fat-fingered unit is visible immediately instead of at the
    next invoice. Integer arithmetic throughout: no float touches a money
    quantity here, displayed or not. Truncating, never rounding, so this line
    can never overstate what was granted.
    """
    dollars, remainder = divmod(credits, CREDITS_PER_USD)
    cents = remainder * 100 // CREDITS_PER_USD
    return f"${dollars}.{cents:02d}"


def grant_idempotency_key(account_id: str) -> str:
    """The ledger key for a seeded grant, keyed on the account ALONE.

    The amount is deliberately NOT in the key. This script is idempotent by
    contract -- its own docstring says so, docs/live-test-auth.md says so, and
    three workflows re-run it as the recovery procedure -- and re-running it is
    the documented fix for the very row that made issue #1599. With the amount
    in the key, a run at 10,000,000,000 followed by a corrected run at
    20,000,000,000 would leave 30,000,000,000 on the account: two correct
    looking operator actions, an unbounded sum, and nothing in the output
    saying the first grant was still there.

    So the unique index on (account_id, entry_type, idempotency_key) is made to
    refuse a second seeded grant whatever amount it carries. The seeder places
    at most one grant per account, ever. A deliberate top-up is a second,
    different decision and belongs on the platform admin grants surface, which
    records who granted it and why.
    """
    return f"demo-seed-grant:{account_id}"


def grant_ledger_row(account_id: str, credits: int) -> dict:
    """The one append-only ledger row that carries a seeded demo grant.

    Every credit this script grants exists as exactly one
    public.credit_ledger_entries row of entry_type 'grant', stating in its own
    metadata why it exists and what wrote it. There is no path here that moves
    a balance without leaving that row behind, and the row is inserted, never
    merged: the ledger is append-only, so an existing entry is left exactly as
    it was written.

    DELIBERATE DEVIATION, read this before copying the pattern. The canonical
    grant path in this repository is grants.Repository.CreateWithLedger
    (apps/control-plane/internal/grants/repository.go), which writes three rows
    in one transaction: public.credit_grants, this ledger entry, and a
    public.credit_idempotency_keys claim. This function writes the middle one
    only. A grant seeded here therefore has no credit_grants row, so no
    granted_by_user_id, no reason_note column, no currency, no grant id, and it
    does NOT appear in grants.Repository.List, which is what the platform admin
    credit grants surface reads. The balance is unaffected: GetBalance sums
    credit_ledger_entries directly.

    Why the deviation: credit_grants.granted_by_user_id names a platform admin,
    and this script deliberately holds no such actor. It writes
    is_platform_admin false on the account it provisions and only ever carries
    the deployment's service role key, never an admin JWT, so there is no
    honest value to put in that column and inventing one would put a fabricated
    granting actor into the audit trail. scripts/ci-seed-api-key.sh makes the
    same call for the same reason. If a seeded grant ever needs to be
    attributable, the right fix is an operator-run grant through the admin
    surface, not a synthetic actor here.
    """
    return {
        "account_id": account_id,
        "entry_type": "grant",
        "credits_delta": credits,
        "idempotency_key": grant_idempotency_key(account_id),
        "metadata": {
            "reason": "operator-specified demo provisioning grant (HIVE_DEMO_CREDITS)",
            "source": "scripts/seed-demo-owner.py",
        },
    }


def provision_credit_grant(request, rest, headers, account_id: str, credits: int) -> None:
    """Post this account's one seeded grant, or say why it did not.

    A function rather than inline in main so the only money write in this
    script has a self-check behind it (scripts/test_seed_demo_owner.py).

    The layering is deliberate and is the part worth preserving if this is ever
    edited. The unique index on (account_id, entry_type, idempotency_key) is
    what ENFORCES one seeded grant per account; the read below is only
    LEGIBILITY, so that a refusal names what the account already holds instead
    of looking like a silent no-op. The insert never trusts that read: it
    carries the conflict target and ignore-duplicates, and the outcome is
    decided by the returned representation, so a read that was stale, racing or
    simply wrong produces a misleading sentence and never a second grant.

    Exits non-zero only when the state is UNKNOWN (a failed lookup, a failed
    write). A refusal is exit zero on purpose: three workflows re-run this
    script as their recovery procedure, and turning a correct no-op into a red
    job would train operators to ignore it.
    """
    status, existing = request(
        rest, headers, "GET", "/credit_ledger_entries",
        params={
            "account_id": f"eq.{account_id}",
            "entry_type": "eq.grant",
            "idempotency_key": f"eq.{grant_idempotency_key(account_id)}",
            "select": "credits_delta",
        },
    )
    if status != 200 or existing is None:
        # A read that failed is not a read that returned nothing.
        print(f"error: existing grant lookup failed: {status} {existing}", file=sys.stderr)
        sys.exit(1)

    if existing:
        held = existing[0]["credits_delta"]
        print(
            f"credit grant: skipped, this account already carries a seeded grant of {held} "
            f"credits ({format_usd_from_credits(held)}). The seeder posts at most one grant "
            f"per account, so the {credits} in HIVE_DEMO_CREDITS was NOT added and the "
            "balance is unchanged. To change the funding, use the platform admin credit "
            "grants surface, which records who granted it and why.",
            file=sys.stderr,
        )
        return

    # Inserted with ignore-duplicates and never merged: the ledger is
    # append-only, so a replay must leave the existing row byte for byte as it
    # was written rather than update it. The key carries no amount, so a second
    # grant of any size collides here and is dropped rather than stacking on
    # the first.
    status, body = request(
        rest, headers, "POST", "/credit_ledger_entries",
        body=grant_ledger_row(account_id, credits),
        params={"on_conflict": "account_id,entry_type,idempotency_key"},
        prefer="resolution=ignore-duplicates,return=representation",
    )
    if status not in (200, 201, 204):
        print(f"error: credit grant failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    if body:
        print(
            f"credit grant: ok ({credits} credits, {format_usd_from_credits(credits)}, posted)",
            file=sys.stderr,
        )
    else:
        print(
            "credit grant: skipped, a concurrent run posted this account's seeded grant "
            f"first. The {credits} in HIVE_DEMO_CREDITS was NOT added.",
            file=sys.stderr,
        )


def find_by_slug(rest, headers, table, slug):
    """Returns the row for slug in table, or None. Used by the tenant/account
    collision guards below -- both tables are upserted by slug with the
    service-role key (bypasses RLS), so a caller must know what it is about
    to merge onto before doing so."""
    status, body = request(
        rest, headers, "GET", f"/{table}",
        params={"select": "*", "slug": f"eq.{slug}"},
    )
    if status != 200:
        print(f"error: {table} slug lookup failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    return body[0] if body else None


def guard_tenant_slug(existing_tenant, foreign_members, own_member):
    """Exits loud unless existing_tenant (found by TENANT_SLUG) is provably
    ours: our own demo user must already be a member (own_member), proof a
    prior run of this exact script created it, AND no other member may be
    present (foreign_members empty) -- see the on_conflict=slug upsert
    comment below for why merging onto it unchecked would be a
    privilege-escalation bug. A tenant with zero members at all is NOT safe
    to reuse just because it has no foreign members: it may be a
    pre-existing tenant this script never touched (never seeded, or fully
    cleaned up), so own_member must be true, not merely foreign_members
    empty (issue #420)."""
    if existing_tenant is not None and (foreign_members or not own_member):
        print(
            f"error: tenant slug {TENANT_SLUG!r} already belongs to tenant "
            f"{existing_tenant['id']} that this script cannot confirm it created -- "
            f"{len(foreign_members)} member(s) that are not the demo user and/or no "
            "membership row proving the demo user already belongs to it. Refusing to "
            "merge onto a tenant this script did not create. Pick a different slug "
            "via HIVE_DEMO_TENANT_SLUG.",
            file=sys.stderr,
        )
        sys.exit(1)


def guard_account_slug(existing_account, foreign_owners, user_id):
    """Exits loud unless existing_account (found by ACCOUNT_SLUG) is provably
    ours: accounts.owner_user_id must already equal our own demo user AND no
    account_memberships row with role='owner' may belong to anyone else
    (foreign_owners empty). control-plane's IsPlatformAdmin authorizes ANY
    owner-role membership on an is_platform_admin account (see
    apps/control-plane/internal/platform/role_pgx.go), not just
    accounts.owner_user_id -- so that single column is not a sufficient
    collision check on its own. The reverse gap is also real (issue #420):
    accounts.owner_user_id is not schema-enforced to match any membership
    row, so an existing account could point owner_user_id at a different
    user with zero owner-role membership rows at all -- foreign_owners
    would be empty and the old guard let that through, then the upsert
    silently replaced owner_user_id and enabled is_platform_admin. Checking
    owner_user_id against our own user_id directly closes that gap."""
    if existing_account is not None and (
        foreign_owners or existing_account.get("owner_user_id") != user_id
    ):
        print(
            f"error: account slug {ACCOUNT_SLUG!r} already belongs to account "
            f"{existing_account['id']} that this script cannot confirm it owns -- "
            f"{len(foreign_owners)} owner-role member(s) that are not the demo user "
            f"and/or owner_user_id={existing_account.get('owner_user_id')!r} does not "
            "match the demo user. Refusing to merge (would silently grant unrelated "
            "user(s) is_platform_admin too, or adopt an account we do not own). Pick a "
            "different slug via HIVE_DEMO_ACCOUNT_SLUG.",
            file=sys.stderr,
        )
        sys.exit(1)


def main() -> None:
    # Parsed before any write, so a typo in a money amount fails the run
    # instead of provisioning everything and then refusing at the last step.
    credits = credits_to_grant(os.environ.get("HIVE_DEMO_CREDITS"))
    supabase_url = env("SUPABASE_URL").rstrip("/")
    service_key = env("SUPABASE_SERVICE_ROLE_KEY")
    headers = {
        "Authorization": f"Bearer {service_key}",
        "apikey": service_key,
        "Content-Type": "application/json",
    }
    rest = supabase_url + "/rest/v1"
    gotrue = supabase_url + "/auth/v1"

    # 1. Find-or-create the GoTrue user first -- the tenant guard below needs
    # user_id to tell "our own demo user" apart from an unrelated tenant's
    # real members. user_metadata.selected_tenant_id is set once the tenant
    # is resolved, further down. `filter=<email>` does an exact server-side
    # match (see scripts/seed-owui-e2e-user.py for the `email=` param 500
    # gotcha on this GoTrue version).
    status, body = request(gotrue, headers, "GET", "/admin/users", params={"filter": USER_EMAIL})
    if status != 200:
        print(f"error: user lookup failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    existing_user = next(
        (u for u in body.get("users", []) if u.get("email", "").lower() == USER_EMAIL.lower()),
        None,
    )

    password = password_to_set(
        existing_user is not None,
        os.environ.get("HIVE_DEMO_PASSWORD", ""),
        random_password(),
    )
    if existing_user is None:
        status, body = request(
            gotrue, headers, "POST", "/admin/users",
            body={"email": USER_EMAIL, "password": password, "email_confirm": True},
        )
        if status not in (200, 201):
            print(f"error: user create failed: {status} {body}", file=sys.stderr)
            sys.exit(1)
        user_id = body["id"]
    else:
        user_id = existing_user["id"]
        if password is None:
            print(
                "password left unchanged (set HIVE_DEMO_PASSWORD to change it); "
                "existing sessions stay valid",
                file=sys.stderr,
            )
        else:
            status, body = request(
                gotrue, headers, "PUT", f"/admin/users/{user_id}", body={"password": password},
            )
            if status != 200:
                print(f"error: user update failed: {status} {body}", file=sys.stderr)
                sys.exit(1)
    print(f"user_id={user_id}", file=sys.stderr)

    # The stdout contract is printed HERE, the moment the identity it describes
    # is committed, rather than at the end of main. For a newly created user
    # that generated password is the account's only credential, and
    # password_to_set deliberately never rotates an existing account's, so a
    # re-run will not mint another one: a run that dies at any later step used
    # to leave an account nobody could sign in to, and hand its caller
    # (.github/workflows/demo-chat-settings-check.yml reads this stdout) nothing
    # to mask or use. Every step below this line is provisioning around an
    # identity that already exists, so nothing after it can invalidate these
    # two lines. Callers that need the whole workspace still have the exit
    # status, which is what they should be branching on.
    print(f"EMAIL={USER_EMAIL}")
    if password is not None:
        print(f"PASSWORD={password}")

    # 2. Guard + upsert the demo tenant. slug is user-chosen; an
    # on_conflict=slug upsert with the service-role key would otherwise
    # silently merge onto ANY pre-existing tenant with this slug -- adding
    # our demo user as OWNER and flipping feature gates on for it, even if
    # that tenant belongs to a real customer. Guard: only a tenant whose
    # only member is our own demo user (proof: a prior run of this exact
    # script) is safe to reuse -- a tenant with zero members is NOT
    # automatically safe, since it may be a pre-existing tenant this script
    # never touched (issue #420). archived_at is force-reset to NULL on
    # every run: this is a dedicated demo tenant this script owns outright,
    # so reactivating it (rather than leaving a demo login unable to get a
    # tenant claim) is the correct default -- see custom_access_token_hook's
    # `t.archived_at IS NULL` filter.
    existing_tenant = find_by_slug(rest, headers, "tenants", TENANT_SLUG)
    if existing_tenant is not None:
        status, members = request(
            rest, headers, "GET", "/tenant_users",
            params={"select": "user_id", "tenant_id": f"eq.{existing_tenant['id']}"},
        )
        if status != 200:
            print(f"error: tenant membership lookup failed: {status} {members}", file=sys.stderr)
            sys.exit(1)
        foreign_members = [m for m in members if m["user_id"] != user_id]
        own_member = any(m["user_id"] == user_id for m in members)
        guard_tenant_slug(existing_tenant, foreign_members, own_member)
        if existing_tenant.get("archived_at"):
            print(
                f"tenant {existing_tenant['id']} was archived_at={existing_tenant['archived_at']}; "
                "reactivating (dedicated demo tenant, safe to un-archive).",
                file=sys.stderr,
            )

    status, body = request(
        rest, headers, "POST", "/tenants",
        body={
            "slug": TENANT_SLUG,
            "name": TENANT_NAME,
            "deployment": TENANT_DEPLOYMENT,
            "archived_at": None,
        },
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: tenant upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    tenant_id = body[0]["id"]
    print(f"tenant_id={tenant_id}", file=sys.stderr)

    # Now that the tenant is resolved, point the user's selected tenant at
    # it. GoTrue admin updateUserById MERGES user_metadata, so this only
    # ever adds/refreshes selected_tenant_id.
    status, body = request(
        gotrue, headers, "PUT", f"/admin/users/{user_id}",
        body={"user_metadata": {"selected_tenant_id": tenant_id}},
    )
    if status != 200:
        print(f"error: user metadata update failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 3. Upsert tenant membership: OWNER unlocks agent-console/edge-api's
    # tenant-admin JWT-role claim (custom_access_token_hook) and is the top
    # role in the tenant_users CHECK constraint.
    status, body = request(
        rest, headers, "POST", "/tenant_users",
        body={
            "tenant_id": tenant_id,
            "user_id": user_id,
            "role": TENANT_ROLE,
            "status": TENANT_STATUS,
        },
        params={"on_conflict": "tenant_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: tenant membership upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 4. Guard + upsert the web-console billing account. This account is
    # deliberately NOT flagged is_platform_admin: after issue #758 the
    # workspace-scoped admin panels (feature gates, marketplace) are reached by
    # the OWNER of the tenant in scope, which step 3 above already granted.
    # Platform-wide powers (credit minting, provider base-URL rewrites) are a
    # deployment-operator concern and a demo account must not hold them; that
    # flag was stripped from this account in production on 2026-08-06 and this
    # script must not put it back. Same slug-collision risk as the tenant guard
    # above: refuse to
    # merge onto an existing account unless owner_user_id already matches
    # our demo user AND no other owner-role member exists -- owner_user_id
    # is not schema-enforced to match any membership row, so checking it
    # directly closes the desync gap the membership-only check missed
    # (issue #420).
    existing_account = find_by_slug(rest, headers, "accounts", ACCOUNT_SLUG)
    if existing_account is not None:
        status, owners = request(
            rest, headers, "GET", "/account_memberships",
            params={
                "select": "user_id",
                "account_id": f"eq.{existing_account['id']}",
                "role": "eq.owner",
            },
        )
        if status != 200:
            print(f"error: account membership lookup failed: {status} {owners}", file=sys.stderr)
            sys.exit(1)
        foreign_owners = [m for m in owners if m["user_id"] != user_id]
        guard_account_slug(existing_account, foreign_owners, user_id)

    status, body = request(
        rest, headers, "POST", "/accounts",
        body={
            "slug": ACCOUNT_SLUG,
            "display_name": ACCOUNT_NAME,
            "account_type": "business",
            "owner_user_id": user_id,
            "is_platform_admin": False,
        },
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: account upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    account_id = body[0]["id"]
    print(f"account_id={account_id}", file=sys.stderr)

    # 5. Upsert account_memberships (web-console's own owner-only page gate,
    # e.g. app/console/billing/budget/page.tsx's `role === "owner"` check).
    status, body = request(
        rest, headers, "POST", "/account_memberships",
        body={
            "account_id": account_id,
            "user_id": user_id,
            "role": "owner",
            "status": "active",
        },
        params={"on_conflict": "account_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: account membership upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 6. Upsert account_profiles with profile_setup_complete=true so the
    # console's onboarding nudge does not stand between login and the demo.
    status, body = request(
        rest, headers, "POST", "/account_profiles",
        body={
            "account_id": account_id,
            "owner_name": ACCOUNT_NAME,
            "login_email": USER_EMAIL,
            "profile_setup_complete": True,
        },
        params={"on_conflict": "account_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: account profile upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 7. Enable the demo-relevant tenant feature gates.
    for key in FEATURE_GATES:
        status, body = request(
            rest, headers, "POST", "/tenant_settings",
            body={
                "tenant_id": tenant_id,
                "key": key,
                "enabled": True,
                "updated_by": user_id,
            },
            params={"on_conflict": "tenant_id,key"},
            prefer="resolution=merge-duplicates",
        )
        if status not in (200, 201, 204):
            print(f"error: feature gate {key} upsert failed: {status} {body}", file=sys.stderr)
            sys.exit(1)

    # 8. Map the tenant to the billing account (issue #1599). Without this row
    # the identity this script provisions can sign in and cannot do anything:
    # edge-api resolves the caller's tenant through
    # public.tenant_billing_accounts and refuses every chat, RAG and voice
    # request with billing_not_configured ("This workspace is not set up for
    # usage yet"), and the credits route answers no balance at all. Same shape
    # as the live signup path, signup.EnsureTenantBillingAccount, and the same
    # omission that made the OWUI shim seed 403 every model listing in #717.
    shared_billing_mapping.provision_billing_mapping(
        request, rest, headers, tenant_id, account_id, BILLING_MAPPING_OPTIONS,
    )

    # 9. Optionally grant credit, and only ever the amount the operator named,
    # and only if this account carries no seeded grant already. Credit on Hive
    # is owner-discretionary: no trial, no signup bonus, no referral reward,
    # and nothing here defaults an amount. Unset means the workspace is
    # provisioned with a zero balance, which is now a legible state end to end
    # rather than a dead end (step 8 plus the zero-balance answer on
    # /internal/chat/credits/balance).
    if credits is None:
        print(
            "credit grant: skipped (HIVE_DEMO_CREDITS unset). The workspace balance stays at "
            "zero, the chat banner shows 'You're out of credits', and the gateway refuses "
            "requests with insufficient_quota until credit is added. Set HIVE_DEMO_CREDITS to a "
            f"positive integer number of credits (1 USD = {CREDITS_PER_USD:,} credits) to fund it.",
            file=sys.stderr,
        )
    else:
        provision_credit_grant(request, rest, headers, account_id, credits)


if __name__ == "__main__":
    main()
