// Service-role seeding for the Playwright E2E fixtures.
//
// This module owns every admin-API mutation the E2E suite needs: the three
// fixture users, their tenants, accounts, memberships, profiles, and the
// single pending invitation. It runs in Node, inside the fixture CLI process
// that `tests/e2e/support/e2e-auth-fixtures.mjs` exposes, and it is the only
// implementation. There is no second copy behind an HTTP boundary.
//
// It used to be a deployed Supabase Edge Function
// (`supabase/functions/e2e-fixtures/`) that the CLI POSTed to. That split put
// the caller and the seeder on independent release cycles: the caller shipped
// with a pull request while the seeder only changed when somebody remembered
// to run `supabase functions deploy`. A caller that started sending the exact
// run-scoped identity it wanted seeded therefore talked to a deployed copy
// that still derived its own, so the specs signed in as a user that was never
// created. Running the same code in-process removes that skew class outright.
//
// Nothing here is browser code. The service-role key stays in this Node
// process and its callers, never reaches page context, and must never be
// written to stdout, stderr, a screenshot, or an artifact. Route any error
// text that could embed it through `redactSecrets` first. That is a rule
// about every printing site in this file, not only the ones at the CLI
// boundary: the sweep below relays raw client-library messages, and those
// messages are exactly where a key or a token ends up embedded.

import { createHash, randomBytes } from "node:crypto";
import { createClient } from "@supabase/supabase-js";

// The fixture identity used when no run key is supplied: local runs, manual
// invocations, anything outside CI. Fixed ids keep repeated local runs
// idempotent against one set of rows.
const LOCAL_IDS = {
  inviterEmail: "e2e-inviter@scubed.com.bd",
  verifiedPrimaryAccountId: "31aadd76-fba0-46e6-827d-e3cfef50324c",
  verifiedSecondaryAccountId: "c420b965-aed6-4bfd-a7f9-e934458b3b5a",
  invitedAccountId: "a45bec1f-e648-4811-9841-3ad28c7f34a9",
  unverifiedAccountId: "8ca58251-dfad-4e91-b2c8-b3649391871b",
  invitationId: "580df639-64b0-4a66-99f1-0cf3e293b78e",
  // Phase 19 tenant-scope role system (public.tenants / public.tenant_users).
  // custom_access_token_hook (20260516_07) raises `no_active_membership` and
  // fails the whole password-grant login for any user with zero active
  // tenant_users rows, independent of the account_memberships rows below: the
  // two role systems do not know about each other (see
  // scripts/seed-demo-owner.py's header comment for the same split).
  // Only verifiedUser and unverifiedUser sign in through Playwright specs
  // (inviterUser is only ever an invitation sender in fixture data, never
  // used in a signIn() flow), so only those two get a tenant.
  verifiedTenantId: "6f1c9a2e-2b7a-4b1a-9a3e-4b2f6a1d7c01",
  unverifiedTenantId: "d3a5f8e1-7c4b-4a9d-8e2f-1b6c9d3a7f02",
  // accounts.slug is UNIQUE. Empty for the unnamespaced (local/default)
  // identity, so the literal slugs below are untouched; a runKey gets a
  // short, stable, per-run suffix so concurrent runs never collide on that
  // constraint the way they used to on accounts.id alone.
  slugSuffix: "",
};

// No DEFAULT_EMAILS constant lives here any more. It used to supply the two
// shared live addresses whenever a caller passed none, which made every
// credential-less local run write a password onto a shared tenant-OWNER
// account through ensureUser below and revoke every session a concurrent run
// was holding. A default that silently targets a shared account is the bug, so
// there is no default: see runScopedEmail.

function sha256Hex(input) {
  return createHash("sha256").update(input, "utf8").digest("hex");
}

// deriveUuid turns an arbitrary string into a stable, valid uuid column
// value: the same input always produces the same output, different inputs
// (almost certainly) do not collide. Good enough for fixture rows that only
// need to be unique per run key, not cryptographically unguessable. Not
// RFC 4122 version/variant compliant, Postgres's uuid type does not check
// those bits.
export function deriveUuid(seed) {
  const hex = sha256Hex(seed);
  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20, 32),
  ].join("-");
}

// withRunKey tags an email's local part (foo@x.com -> foo+key@x.com) so a
// namespaced run gets a genuinely distinct auth.users row rather than
// colliding with the shared default identity or another run's.
export function withRunKey(email, runKey) {
  const at = email.indexOf("@");
  if (at === -1) return `${email}+${runKey}`;
  return `${email.slice(0, at)}+${runKey}${email.slice(at)}`;
}

// runScopedEmail is the gate on every address this module writes a password
// to. ensureUser sends `password:` on both of its update paths, so an address
// that is not scoped to this run means overwriting the credential of an
// account other runs are signed in as. There is no fallback and no shared
// default: an empty run key throws, and an address that does not already carry
// the key gets it. Idempotent, because ci.yml passes an address that is
// already namespaced alongside the same key.
export function runScopedEmail(email, runKey) {
  if (typeof email !== "string" || email === "") {
    throw new Error(
      "E2E fixture seeding requires an explicit address for every fixture " +
        "user. It has no default, because the default was a shared live account."
    );
  }
  if (!runKey) {
    throw new Error(
      "E2E fixture seeding requires E2E_RUN_KEY. Without it the fixtures " +
        "target shared live accounts, and seeding overwrites their passwords, " +
        "which revokes every session other runs are holding (see " +
        "docs/live-test-auth.md). Export any unique value, for example " +
        "E2E_RUN_KEY=$(whoami)-$(date +%s)."
    );
  }
  return email.includes(`+${runKey}@`) ? email : withRunKey(email, runKey);
}

// buildIds returns LOCAL_IDS unchanged when runKey is empty, or a full set of
// ids deterministically derived from runKey when it is set. CI passes one run
// key per job attempt, so two concurrent jobs get entirely disjoint rows in
// every table this module touches and can never race each other the way a
// single shared identity set did.
export function buildIds(runKey) {
  if (!runKey) return LOCAL_IDS;
  return {
    inviterEmail: withRunKey(LOCAL_IDS.inviterEmail, runKey),
    verifiedPrimaryAccountId: deriveUuid(`verifiedPrimaryAccount:${runKey}`),
    verifiedSecondaryAccountId: deriveUuid(
      `verifiedSecondaryAccount:${runKey}`
    ),
    invitedAccountId: deriveUuid(`invitedAccount:${runKey}`),
    unverifiedAccountId: deriveUuid(`unverifiedAccount:${runKey}`),
    invitationId: deriveUuid(`invitation:${runKey}`),
    verifiedTenantId: deriveUuid(`verifiedTenant:${runKey}`),
    unverifiedTenantId: deriveUuid(`unverifiedTenant:${runKey}`),
    slugSuffix: `-${deriveUuid(`slug:${runKey}`).slice(0, 8)}`,
  };
}

// Parameter names that carry a credential. They are matched wherever a
// `name=value` pair appears, which deliberately includes a URL FRAGMENT and
// not only a query string.
//
// 2026-08-08 incident: an agent's own redactor split URLs on "?" and scrubbed
// query parameters only. GoTrue hands a session back from
// GET /auth/v1/verify as a redirect whose credentials live after the "#"
// (`...#access_token=<jwt>&refresh_token=...`), so that redactor printed a
// live session token to stdout while looking like it was protecting the log.
// Anything that only understands query strings is not a redactor.
const CREDENTIAL_PARAMS = [
  "access_token",
  "refresh_token",
  "provider_token",
  "provider_refresh_token",
  "id_token",
  "token_hash",
  "hashed_token",
  "confirmation_token",
  "recovery_token",
  "invitation_token",
  "invite_token",
  "email_otp",
  "client_secret",
  "api_key",
  "apikey",
  "password",
  "secret",
  "token",
  "code",
  "otp",
];
// Longest names first so `provider_refresh_token` never degrades to `token`.
const CREDENTIAL_PARAM_RE = new RegExp(
  `\\b(${[...CREDENTIAL_PARAMS].sort((a, b) => b.length - a.length).join("|")})=([^&#\\s"'\\\\]+)`,
  "gi"
);
// A bare JWT with no parameter name around it: an access token logged on its
// own line, an `Authorization: Bearer ...` header, or a service-role key that
// was passed in rather than read from the environment.
const BARE_JWT_RE = /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}/g;

// redactSecrets scrubs the service-role key (and anything else worth hiding)
// out of text that is about to be printed. The fixture CLI runs as a child
// process whose stdout and stderr are relayed into the CI log on failure, so
// every message that leaves this module goes through here first, as does
// every message from live-auth.mjs. It is safe to run with verbose logging.
export function redactSecrets(text, secrets = [process.env.SUPABASE_SERVICE_ROLE_KEY]) {
  let out = String(text);
  for (const secret of secrets) {
    if (typeof secret === "string" && secret.length > 0) {
      out = out.split(secret).join("<redacted>");
    }
  }
  out = out.replace(CREDENTIAL_PARAM_RE, (_match, name) => `${name}=<redacted>`);
  out = out.replace(BARE_JWT_RE, "<redacted>");
  return out;
}

// createAdminClient is deliberately the only way into this module, and it
// throws rather than returning a no-op client. Seeding has no fallback path
// any more: a run without admin credentials cannot create the identity the
// specs sign in as, so it must fail here and name the missing variable
// instead of leaving the suite to time out 25 seconds later on a sign-in for
// a user that was never created.
export function createAdminClient() {
  const url = process.env.SUPABASE_URL;
  const serviceRoleKey = process.env.SUPABASE_SERVICE_ROLE_KEY;
  const missing = [
    url ? null : "SUPABASE_URL",
    serviceRoleKey ? null : "SUPABASE_SERVICE_ROLE_KEY",
  ].filter(Boolean);
  if (missing.length > 0) {
    throw new Error(
      `E2E fixture seeding requires ${missing.join(" and ")}. Seeding runs ` +
        "in-process against the Supabase admin API and has no fallback, so a " +
        "missing variable fails the run rather than leaving it unseeded."
    );
  }
  return createClient(url, serviceRoleKey, {
    auth: { persistSession: false, autoRefreshToken: false },
    global: { fetch: timedFetch },
  });
}

// Ceiling for one admin API call. Node's fetch has no request deadline of its
// own (only a 300s headers timeout), so a stalled call sits there until
// fixture-reset.ts kills the whole seeder child at 120s with a bare signal,
// which says nothing about which call was stuck. A full seed is roughly 40
// calls at tens of milliseconds each, so 25s is far outside anything healthy
// while still landing inside that outer kill. What it buys is one line naming
// the endpoint that stalled.
const ADMIN_CALL_TIMEOUT_MS = 25_000;

function callTimeoutMs() {
  // Overridable so the unit test can prove the message without waiting 25s,
  // and so a run against a deliberately slow environment can be given room
  // without editing this file.
  const raw = Number(process.env.E2E_FIXTURE_CALL_TIMEOUT_MS);
  return Number.isFinite(raw) && raw > 0 ? raw : ADMIN_CALL_TIMEOUT_MS;
}

export async function timedFetch(input, init = {}) {
  // supabase-js passes a string today, but fetch takes a string, a URL or a
  // Request, and the label must not be the thing that throws. A Request keeps
  // its own method when init does not override it; a URL stringifies (reading
  // `.url` off one yields undefined, which `new URL` rejects).
  const request =
    typeof Request !== "undefined" && input instanceof Request
      ? input
      : undefined;
  const url = new URL(request?.url ?? String(input));
  const method = init.method ?? request?.method ?? "GET";
  // Path only. PostgREST puts filter values in the query string and some of
  // them are fixture email addresses, so the query is deliberately dropped
  // rather than redacted: this repository is public and CI logs are with it.
  const label = `${method} ${url.pathname}`;
  const startedAt = Date.now();
  const timeoutMs = callTimeoutMs();
  // gotrue-js passes its own signal on some admin calls, so compose rather
  // than replace: dropping theirs would leave those calls unabortable.
  const deadline = AbortSignal.timeout(timeoutMs);
  const signal = init.signal
    ? AbortSignal.any([init.signal, deadline])
    : deadline;
  try {
    return await fetch(input, { ...init, signal });
  } catch (err) {
    if (deadline.aborted) {
      throw new Error(
        `[e2e-fixture-seed] admin API call stalled: ${label} did not answer ` +
          `within ${timeoutMs}ms (waited ${Date.now() - startedAt}ms)`
      );
    }
    throw err;
  }
}

async function ensureUser(admin, opts) {
  const userMetadata = { full_name: opts.fullName };
  if (opts.selectedTenantId) {
    // custom_access_token_hook reads the tenant claim off
    // auth.users.raw_user_meta_data->>'selected_tenant_id'.
    userMetadata.selected_tenant_id = opts.selectedTenantId;
  }

  const { data: ownerRow } = await admin
    .from("accounts")
    .select("owner_user_id")
    .eq("id", opts.accountIdHint)
    .maybeSingle();

  if (ownerRow?.owner_user_id) {
    const { data, error } = await admin.auth.admin.updateUserById(
      ownerRow.owner_user_id,
      {
        email: opts.email,
        password: opts.password,
        email_confirm: opts.emailConfirm,
        app_metadata: opts.appMetadata,
        user_metadata: userMetadata,
      }
    );
    if (error) throw new Error(`updateUserById failed: ${error.message}`);
    return data.user;
  }

  const { data, error } = await admin.auth.admin.createUser({
    email: opts.email,
    password: opts.password,
    email_confirm: opts.emailConfirm,
    app_metadata: opts.appMetadata,
    user_metadata: userMetadata,
  });
  if (!error) return data.user;

  // 422/400 here means the address already exists on a user this account's
  // owner_user_id does not point at (a previous run created it, then the
  // account row was swept). Find it by address and update it in place.
  if (error.status !== 422 && error.status !== 400) {
    throw new Error(`createUser failed: ${error.message}`);
  }
  const { data: list, error: listErr } = await admin.auth.admin.listUsers({
    page: 1,
    perPage: 1000,
  });
  if (listErr) throw new Error(`listUsers failed: ${listErr.message}`);
  const existing = list.users.find(
    (u) => u.email?.toLowerCase() === opts.email.toLowerCase()
  );
  if (!existing) throw new Error(error.message);
  const { data: upd, error: updErr } = await admin.auth.admin.updateUserById(
    existing.id,
    {
      email: opts.email,
      password: opts.password,
      email_confirm: opts.emailConfirm,
      app_metadata: opts.appMetadata,
      user_metadata: userMetadata,
    }
  );
  if (updErr) throw new Error(`updateUserById failed: ${updErr.message}`);
  return upd.user;
}

async function seedTenantsAndMemberships(admin, ids, users) {
  const { verifiedUser, unverifiedUser } = users;

  const { error: tenantErr } = await admin.from("tenants").upsert(
    [
      {
        id: ids.verifiedTenantId,
        slug: `e2e-verified-tenant${ids.slugSuffix}`,
        name: "E2E Verified Tenant",
        deployment: "HIVE_CLOUD",
        archived_at: null,
      },
      {
        id: ids.unverifiedTenantId,
        slug: `e2e-unverified-tenant${ids.slugSuffix}`,
        name: "E2E Unverified Tenant",
        deployment: "HIVE_CLOUD",
        archived_at: null,
      },
    ],
    { onConflict: "id" }
  );
  if (tenantErr) throw new Error(`tenants upsert failed: ${tenantErr.message}`);

  const { error: tenantUserErr } = await admin.from("tenant_users").upsert(
    [
      {
        tenant_id: ids.verifiedTenantId,
        user_id: verifiedUser.id,
        role: "OWNER",
        status: "ACTIVE",
      },
      {
        tenant_id: ids.unverifiedTenantId,
        user_id: unverifiedUser.id,
        role: "OWNER",
        status: "ACTIVE",
      },
    ],
    { onConflict: "tenant_id,user_id" }
  );
  if (tenantUserErr) {
    throw new Error(`tenant_users upsert failed: ${tenantUserErr.message}`);
  }
}

// The credits each fixture tenant is funded with. Session chat takes a flat
// 10000 credit hold before it dispatches, so an account below that figure is
// refused on quota and never reaches a provider. This is deliberately several
// holds' worth: a spec that sends more than one turn must not start failing on
// the second.
const FIXTURE_GRANT_CREDITS = 1_000_000;

// seedBilling maps each fixture tenant to the account it settles against and
// funds that account.
//
// Before #746 the session chat path took no hold and issued no charge, so a
// fixture tenant with no tenant_billing_accounts row and no credits still got
// served. It is now refused with billing_not_configured, which is the correct
// production behaviour and the wrong fixture: a test tenant that cannot be
// billed does not exercise a billing gateway at all, it exercises the refusal
// in front of it. Seeding the mapping and the balance makes the suite run the
// metered path the way a real tenant does, hold and settlement included.
//
// Both writes are upserts on their natural keys, so a re-seed of the fixed
// local identity is idempotent and never double funds an account. The grant's
// idempotency key is per account and carries no run entropy for exactly that
// reason: the unique index on (account_id, entry_type, idempotency_key) is
// what makes a repeated seed a no-op instead of a second million credits.
//
// Runs after seedAccountsAndMemberships and seedTenantsAndMemberships, because
// tenant_billing_accounts has a foreign key into each.
async function seedBilling(admin, ids) {
  const pairs = [
    { tenant_id: ids.verifiedTenantId, account_id: ids.verifiedPrimaryAccountId },
    { tenant_id: ids.unverifiedTenantId, account_id: ids.unverifiedAccountId },
  ];

  const { error: mapErr } = await admin
    .from("tenant_billing_accounts")
    .upsert(pairs, { onConflict: "tenant_id" });
  if (mapErr) {
    throw new Error(`tenant_billing_accounts upsert failed: ${mapErr.message}`);
  }

  const { error: grantErr } = await admin.from("credit_ledger_entries").upsert(
    pairs.map((pair) => ({
      account_id: pair.account_id,
      entry_type: "grant",
      credits_delta: FIXTURE_GRANT_CREDITS,
      idempotency_key: `e2e-fixture-grant:${pair.account_id}`,
      metadata: { source: "e2e-fixture-seed" },
    })),
    { onConflict: "account_id,entry_type,idempotency_key", ignoreDuplicates: true }
  );
  if (grantErr) {
    throw new Error(`credit_ledger_entries grant upsert failed: ${grantErr.message}`);
  }
}

async function seedAccountsAndMemberships(admin, ids, users) {
  const { verifiedUser, unverifiedUser, inviterUser } = users;

  const { error: accErr } = await admin.from("accounts").upsert(
    [
      {
        id: ids.verifiedPrimaryAccountId,
        slug: `e2e-verified-workspace${ids.slugSuffix}`,
        display_name: "E2E Verified Workspace",
        account_type: "personal",
        owner_user_id: verifiedUser.id,
      },
      {
        id: ids.verifiedSecondaryAccountId,
        slug: `e2e-shared-workspace${ids.slugSuffix}`,
        display_name: "E2E Shared Workspace",
        account_type: "personal",
        owner_user_id: inviterUser.id,
      },
      {
        id: ids.invitedAccountId,
        slug: `e2e-invited-workspace${ids.slugSuffix}`,
        display_name: "E2E Invited Workspace",
        account_type: "personal",
        owner_user_id: inviterUser.id,
      },
      {
        id: ids.unverifiedAccountId,
        slug: `e2e-unverified-workspace${ids.slugSuffix}`,
        display_name: "E2E Unverified Workspace",
        account_type: "personal",
        owner_user_id: unverifiedUser.id,
      },
    ],
    { onConflict: "id" }
  );
  if (accErr) throw new Error(`accounts upsert failed: ${accErr.message}`);

  // Only one (account_id, user_id) pair needs to be cleared rather than
  // upserted: verifiedUser's membership in the invited workspace, which must
  // start each reset UNACCEPTED so the invitation-acceptance spec has
  // something to accept. Every other pair below is already going to be
  // written by the upsert immediately after this, so it never needs a
  // separate delete first.
  //
  // This used to delete all six pairs, then upsert five of them back a
  // moment later in a second, non-atomic call. Between those two calls every
  // one of those rows briefly had zero memberships. A read landing in that
  // window (this job's own, or a concurrent job's, hitting these same fixed
  // ids) saw an empty membership list and tripped control-plane's
  // EnsureViewerContext into provisionDefaultWorkspace, handing the signed-in
  // user a brand new, unrelated workspace instead of the fixture's intended
  // one. That is what produced auth-shell.spec.ts's "members page redirects
  // unverified users" failure: a genuine server error, not stale data, from a
  // page whose Promise.all had nothing to catch it.
  const { error: clearErr } = await admin
    .from("account_memberships")
    .delete()
    .eq("account_id", ids.invitedAccountId)
    .eq("user_id", verifiedUser.id);
  if (clearErr) {
    throw new Error(
      `membership delete failed (${ids.invitedAccountId}/${verifiedUser.id}): ${clearErr.message}`
    );
  }

  // Fixes issue #608: account_memberships.created_at defaults to now(), and a
  // single multi-row upsert statement stamps every inserted row with that one
  // transaction timestamp: there is no wall-clock gap between them.
  // ListMembershipsByUserID
  // (apps/control-plane/internal/accounts/repository.go) orders by
  // `created_at ASC, id ASC`, with id documented there as a tiebreaker "only
  // for the pathological case of two memberships sharing one created_at". A
  // bulk upsert is exactly that pathological case on every seed call, so
  // verifiedUser's two memberships below (owner of the primary workspace,
  // plain member of the shared one) tied on created_at and fell back to
  // comparing each row's freshly random gen_random_uuid() id: a coin flip on
  // every reseed, not a fixed order within one run. EnsureViewerContext takes
  // memberships[0] as the signed-in user's default workspace whenever no
  // `hive_account_id` cookie is set, which is every spec's first navigation
  // after signIn(), so roughly half of all CI runs landed the verified spec
  // user in the workspace they only have "member" role in. That is the
  // confirmed cause of profile-completion.spec.ts's "billing settings save
  // partial business profile" (an owner-only route) failing intermittently.
  //
  // Fixed at the source that produces the tie, not at the consumer that
  // resolves it: the Go-side id tiebreak is correct general-purpose behavior
  // (real production data can have two memberships insert in one statement
  // too) and its own regression test already pins that shape, so it stays as
  // a last-resort tiebreak rather than becoming the primary sort. Stamping
  // created_at explicitly here, one millisecond apart in the intended
  // priority order (verifiedUser's owned workspace before the one they only
  // joined), means the seeded rows never reach that pathological case at all,
  // on every seed call including reseeds of the same run key across
  // Playwright retries.
  const membershipSeedTime = Date.now();
  const { error: memErr } = await admin.from("account_memberships").upsert(
    [
      {
        account_id: ids.verifiedPrimaryAccountId,
        user_id: verifiedUser.id,
        role: "owner",
        status: "active",
      },
      {
        account_id: ids.verifiedSecondaryAccountId,
        user_id: verifiedUser.id,
        role: "member",
        status: "active",
      },
      {
        account_id: ids.unverifiedAccountId,
        user_id: unverifiedUser.id,
        role: "owner",
        status: "active",
      },
      {
        account_id: ids.verifiedSecondaryAccountId,
        user_id: inviterUser.id,
        role: "owner",
        status: "active",
      },
      {
        account_id: ids.invitedAccountId,
        user_id: inviterUser.id,
        role: "owner",
        status: "active",
      },
    ].map((membership, index) => ({
      ...membership,
      created_at: new Date(membershipSeedTime + index).toISOString(),
    })),
    { onConflict: "account_id,user_id" }
  );
  if (memErr) throw new Error(`memberships upsert failed: ${memErr.message}`);
}

async function resetProfilesAndInvitation(
  admin,
  ids,
  users,
  invitationTokenHash,
  verifiedEmail,
  unverifiedEmail
) {
  const { inviterUser } = users;

  const { error: profErr } = await admin.from("account_profiles").upsert(
    [
      {
        account_id: ids.verifiedPrimaryAccountId,
        owner_name: "E2E Verified Owner",
        login_email: verifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: ids.verifiedSecondaryAccountId,
        owner_name: "E2E Shared Owner",
        login_email: verifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: ids.invitedAccountId,
        owner_name: "E2E Inviter Owner",
        login_email: verifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: ids.unverifiedAccountId,
        owner_name: "E2E Unverified Owner",
        login_email: unverifiedEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
    ],
    { onConflict: "account_id" }
  );
  if (profErr) throw new Error(`profiles upsert failed: ${profErr.message}`);

  // Audited for issue #592 (a plain delete with no paired upsert, unlike the
  // account_memberships and account_invitations fixes above and below): left
  // as a bare delete on purpose, not a bug needing a guard.
  //
  // control-plane's GetBillingProfile LEFT JOINs account_billing_profiles and
  // COALESCEs every column, so a missing row after this delete reads back as
  // blank fields, never ErrNotFound — there is nothing here for a concurrent
  // read to observe as broken. And there is no concurrent read to begin with:
  // the fixture CLI runs as a child process that every caller awaits to
  // completion before its test body starts (tests/e2e/support/fixture-reset.ts)
  // from a single Playwright worker (playwright.config.ts pins workers: 1 for
  // exactly this reason), and the context/page fixtures are not built until
  // after the last beforeEach returns. No spec's page load can ever land inside
  // this function's execution window, in this run or a concurrent one (account
  // ids are namespaced per E2E_RUN_KEY, so two jobs never touch the same row).
  //
  // This used to say "runs via execFileSync, fully synchronous". It no longer
  // does: blocking the worker's event loop also froze Playwright's timeout
  // timer, so a test could overrun its deadline and still be reported as
  // passed. The guarantee above never depended on the loop being blocked, only
  // on the await and on workers: 1. Revisit if workers ever goes above 1, or if
  // a caller stops awaiting this CLI before touching a page.
  const accountIds = [
    ids.verifiedPrimaryAccountId,
    ids.verifiedSecondaryAccountId,
    ids.invitedAccountId,
    ids.unverifiedAccountId,
  ];
  for (const accountId of accountIds) {
    const { error } = await admin
      .from("account_billing_profiles")
      .delete()
      .eq("account_id", accountId);
    if (error) {
      throw new Error(
        `billing profile delete failed (${accountId}): ${error.message}`
      );
    }
  }

  // No delete before this upsert: onConflict on id already rewrites every
  // field (including accepted_at back to null) in one atomic statement, the
  // same shape of fix as the account_memberships one above.
  const { error: upInvErr } = await admin.from("account_invitations").upsert(
    [
      {
        id: ids.invitationId,
        account_id: ids.invitedAccountId,
        email: verifiedEmail,
        role: "member",
        token_hash: invitationTokenHash,
        expires_at: "2099-01-01T00:00:00Z",
        accepted_at: null,
        invited_by_user_id: inviterUser.id,
      },
    ],
    { onConflict: "id" }
  );
  if (upInvErr) throw new Error(`invitation upsert failed: ${upInvErr.message}`);
}

// A CI job's run key is only ever reused by retries of that exact job
// attempt, never by a later one, so any namespaced fixture row older than a
// job could plausibly still be running is abandoned. Swept on every seed call
// rather than on a schedule or a dedicated teardown step, so it needs no new
// CI wiring and runs from the one entrypoint every caller already goes
// through.
const STALE_RUN_HOURS = 3;

async function sweepStaleFixtureRuns(admin) {
  const cutoffIso = new Date(
    Date.now() - STALE_RUN_HOURS * 3600 * 1000
  ).toISOString();

  // Both conditions matter: the slug is the belt (every account this module
  // ever creates is prefixed "e2e-", nothing else in this project is), the
  // "+" email check on the profile below is the suspenders (rules out a real
  // customer account that happens to also start with that prefix). Sweeping
  // on the email pattern alone would delete any real account whose owner uses
  // a "+" Gmail-style address, which is common enough in the wild that this
  // is not a hypothetical.
  const { data: oldAccounts, error: accErr } = await admin
    .from("accounts")
    .select("id, owner_user_id")
    .like("slug", "e2e-%")
    .lt("created_at", cutoffIso);
  if (accErr || !oldAccounts?.length) return;

  const { data: profiles, error: profErr } = await admin
    .from("account_profiles")
    .select("account_id")
    .in(
      "account_id",
      oldAccounts.map((a) => a.id)
    )
    .like("login_email", "%+%@%");
  if (profErr || !profiles?.length) return;

  const staleAccountIds = new Set(profiles.map((p) => p.account_id));
  const stale = oldAccounts.filter((a) => staleAccountIds.has(a.id));

  for (const acct of stale) {
    // tenant_billing_accounts.account_id is ON DELETE RESTRICT on purpose:
    // deleting an account that still funds a live tenant must fail loudly
    // rather than orphan that tenant into unmetered service. A swept fixture
    // account is the one case where dropping the mapping is correct, so the
    // sweep unmaps it explicitly. Without this the account delete below fails
    // the foreign key, the loop logs and skips, and stale fixture rows
    // accumulate forever.
    const { error: delMapErr } = await admin
      .from("tenant_billing_accounts")
      .delete()
      .eq("account_id", acct.id);
    if (delMapErr) {
      console.error(
        redactSecrets(
          `sweep: unmap billing account ${acct.id} failed: ${delMapErr.message}`
        )
      );
      continue;
    }
    // accounts.id cascades to every account-scoped table (memberships,
    // profiles, billing, invitations, credits, api keys). Deleting it first
    // is what makes the user delete below legal: accounts.owner_user_id has
    // no ON DELETE CASCADE, so a user who still owns an account cannot be
    // deleted.
    const { error: delAcctErr } = await admin
      .from("accounts")
      .delete()
      .eq("id", acct.id);
    if (delAcctErr) {
      console.error(
        redactSecrets(`sweep: delete account ${acct.id} failed: ${delAcctErr.message}`)
      );
      continue;
    }
    const { error: delUserErr } = await admin.auth.admin.deleteUser(
      acct.owner_user_id
    );
    if (delUserErr) {
      console.error(
        redactSecrets(
          `sweep: delete user ${acct.owner_user_id} failed: ${delUserErr.message}`
        )
      );
    }
  }
}

// seedFixtures produces the same deterministic state on every call: the three
// fixture users exist with the passed credentials, one invitation is pending
// for the verified user, and every profile or billing mutation a prior test
// made is cleared. The caller passes the identity it already resolved, so
// there is exactly one derivation of these strings in the whole suite.
export async function seedFixtures(admin, opts) {
  const runKey = typeof opts.runKey === "string" ? opts.runKey.trim() : "";
  const ids = buildIds(runKey);
  // Throws on an empty run key, so LOCAL_IDS above is now only ever reached by
  // a caller that has already been rejected here.
  const verifiedEmail = runScopedEmail(opts.verifiedEmail, runKey);
  const unverifiedEmail = runScopedEmail(opts.unverifiedEmail, runKey);

  await sweepStaleFixtureRuns(admin);

  const [verifiedUser, unverifiedUser, inviterUser] = await Promise.all([
    ensureUser(admin, {
      email: verifiedEmail,
      password: opts.verifiedPassword,
      emailConfirm: true,
      appMetadata: { hive_email_verified: true },
      fullName: "E2E Verified Owner",
      accountIdHint: ids.verifiedPrimaryAccountId,
      selectedTenantId: ids.verifiedTenantId,
    }),
    ensureUser(admin, {
      email: unverifiedEmail,
      password: opts.unverifiedPassword,
      emailConfirm: true,
      appMetadata: { hive_email_verified: false },
      fullName: "E2E Unverified Owner",
      accountIdHint: ids.unverifiedAccountId,
      selectedTenantId: ids.unverifiedTenantId,
    }),
    ensureUser(admin, {
      email: ids.inviterEmail,
      password: randomBytes(24).toString("hex"),
      emailConfirm: true,
      appMetadata: { hive_email_verified: true },
      fullName: "E2E Inviter Owner",
      accountIdHint: ids.verifiedSecondaryAccountId,
    }),
  ]);

  const users = { verifiedUser, unverifiedUser, inviterUser };
  await seedAccountsAndMemberships(admin, ids, users);
  await seedTenantsAndMemberships(admin, ids, users);
  await seedBilling(admin, ids);
  await resetProfilesAndInvitation(
    admin,
    ids,
    users,
    sha256Hex(opts.invitationToken),
    verifiedEmail,
    unverifiedEmail
  );

  return {
    verifiedEmail,
    unverifiedEmail,
    verifiedUserId: verifiedUser.id,
    unverifiedUserId: unverifiedUser.id,
    inviterUserId: inviterUser.id,
    verifiedPrimaryAccountId: ids.verifiedPrimaryAccountId,
    verifiedSecondaryAccountId: ids.verifiedSecondaryAccountId,
    invitedAccountId: ids.invitedAccountId,
    unverifiedAccountId: ids.unverifiedAccountId,
  };
}

// resetProfileComplete clears profile_setup_complete on every fixture profile
// carrying this login_email. Used between tests in a serial file where an
// earlier test completed the setup form and a later one needs the "Complete
// setup" call to action back.
//
// Deliberately updates all matching rows rather than picking one. The verified
// tester's address is the login_email on three of the seeded accounts (their
// own workspace plus the two the inviter owns), so a single-row update had to
// guess which one the spec would land on. Setting the flag false on all of
// them is what the full seed does anyway, and matches every account the spec
// could reach.
export async function resetProfileComplete(admin, email) {
  const { data, error } = await admin
    .from("account_profiles")
    .update({ profile_setup_complete: false })
    .eq("login_email", email)
    .select("account_id");
  if (error) throw new Error(`profile reset failed: ${error.message}`);
  if (!data?.length) {
    throw new Error(`no fixture profile found for login_email: ${email}`);
  }
  return { email, accountIds: data.map((row) => row.account_id) };
}
