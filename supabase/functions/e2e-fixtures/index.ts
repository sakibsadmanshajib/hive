// Supabase Edge Function: e2e-fixtures
//
// Server-side seed + reset of E2E test users, accounts, memberships,
// profiles, and a single pending invitation. Replaces the per-test
// admin-API round-tripping that used to live in
// `apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs` and keeps
// the service-role key out of CI runners entirely — Playwright only
// needs the edge function's shared `E2E_FIXTURE_SECRET`.
//
// Admin client key precedence: projects with asymmetric JWT signing keys
// (ES256) enabled reject the legacy auto-injected `SUPABASE_SERVICE_ROLE_KEY`
// on GoTrue's admin API (`unrecognized JWT kid`). Supabase auto-injects a
// parallel `SUPABASE_SECRET_KEYS` (JSON, keyed by name, e.g. `{"default":
// "sb_secret_..."}`) alongside it; that new-format key is preferred when
// present, with `SUPABASE_SERVICE_ROLE_KEY` kept as the fallback for
// self-hosted/local projects still on the legacy scheme. See
// https://supabase.com/docs/guides/getting-started/migrating-to-new-api-keys
// and https://supabase.com/docs/guides/functions/secrets.
//
// Deploy:
//   supabase functions deploy e2e-fixtures \
//     --project-ref <ref> --no-verify-jwt
//   supabase secrets set E2E_FIXTURE_SECRET=<long-random-string>
//
// Caller contract:
//   POST /functions/v1/e2e-fixtures
//   Headers: X-E2E-Secret: <E2E_FIXTURE_SECRET>
//   Body:    { "action": "reset", "runKey"?: string, "verifiedEmail"?: string,
//              "unverifiedEmail"?: string, "invitationToken"?: string }
//   200:     { verifiedEmail, unverifiedEmail, verifiedPassword,
//              unverifiedPassword, invitationToken, verifiedUserId,
//              unverifiedUserId, inviterUserId,
//              verifiedPrimaryAccountId, verifiedSecondaryAccountId,
//              invitedAccountId, unverifiedAccountId }
//
// Every call produces the same deterministic state: the three test
// users exist and are password-reset, one invitation is pending for
// the verified user, and all profile/billing mutations from prior
// runs are cleared.
//
// runKey namespaces every id and email this call touches (see buildIds
// below), so concurrent callers with different run keys never share a row.
// Omit it (every caller before this field existed, and any local/manual
// call) to get the single shared fixture identity this function always
// used to manage; that behaviour is unchanged. CI sets runKey to one value
// per job attempt and additionally passes the exact verifiedEmail /
// unverifiedEmail / invitationToken its Playwright processes already
// resolved, so this function seeds precisely what the specs expect instead
// of the two sides deriving the same run-scoped string independently and
// risking drift. Every reset call also opportunistically deletes any
// run-key-derived fixture rows older than a few hours (sweepStaleFixtureRuns
// below), so a namespaced run's rows do not accumulate in this project
// forever even without a dedicated teardown step.

// deno-lint-ignore-file no-explicit-any
import { createClient } from "npm:@supabase/supabase-js@2";

// The plain object every caller used to import directly. Kept as the return
// value of buildIds("") (no run key) below, so a caller that never heard of
// run-key isolation gets byte-identical ids to before.
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
  // tenant_users rows, independent of the account_memberships rows above --
  // the two role systems don't know about each other (see
  // scripts/seed-demo-owner.py's header comment for the same split).
  // Only verifiedUser and unverifiedUser sign in through Playwright specs
  // (inviterUser is only ever an invitation-sender in fixture data, never
  // used in a signIn() flow), so only those two get a tenant.
  verifiedTenantId: "6f1c9a2e-2b7a-4b1a-9a3e-4b2f6a1d7c01",
  unverifiedTenantId: "d3a5f8e1-7c4b-4a9d-8e2f-1b6c9d3a7f02",
  // accounts.slug is UNIQUE. Empty for the unnamespaced (local/default)
  // identity, so the original literal slugs below are untouched; a runKey
  // gets a short, stable, per-run suffix so concurrent runs never collide
  // on that constraint the way they used to on accounts.id alone.
  slugSuffix: "",
};

// deriveUuid turns an arbitrary string into a stable, valid uuid column
// value: same input always produces the same output, different inputs
// (almost certainly) don't collide. Good enough for fixture rows that only
// need to be unique per run key, not cryptographically unguessable. Not
// RFC 4122 version/variant compliant, Postgres's uuid type doesn't check
// those bits.
async function deriveUuid(seed: string): Promise<string> {
  const hex = await sha256Hex(seed);
  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20, 32),
  ].join("-");
}

// buildIds returns LOCAL_IDS unchanged when runKey is empty (every existing
// caller: local dev, and any request that predates run-key isolation), or a
// full set of ids deterministically derived from runKey when it is set (CI,
// one runKey per job attempt). Two concurrent callers with different run
// keys get entirely disjoint rows in every table this function touches, so
// they can never race each other the way a single shared identity set did.
async function buildIds(runKey: string): Promise<typeof LOCAL_IDS> {
  if (!runKey) return LOCAL_IDS;
  const [
    verifiedPrimaryAccountId,
    verifiedSecondaryAccountId,
    invitedAccountId,
    unverifiedAccountId,
    invitationId,
    verifiedTenantId,
    unverifiedTenantId,
  ] = await Promise.all([
    deriveUuid(`verifiedPrimaryAccount:${runKey}`),
    deriveUuid(`verifiedSecondaryAccount:${runKey}`),
    deriveUuid(`invitedAccount:${runKey}`),
    deriveUuid(`unverifiedAccount:${runKey}`),
    deriveUuid(`invitation:${runKey}`),
    deriveUuid(`verifiedTenant:${runKey}`),
    deriveUuid(`unverifiedTenant:${runKey}`),
  ]);
  const slugSuffix = `-${(await deriveUuid(`slug:${runKey}`)).slice(0, 8)}`;
  return {
    inviterEmail: withRunKey(LOCAL_IDS.inviterEmail, runKey),
    verifiedPrimaryAccountId,
    verifiedSecondaryAccountId,
    invitedAccountId,
    unverifiedAccountId,
    invitationId,
    verifiedTenantId,
    unverifiedTenantId,
    slugSuffix,
  };
}

// withRunKey tags an email's local part (foo@x.com -> foo+key@x.com) so a
// namespaced run gets a genuinely distinct auth.users row rather than
// colliding with the shared default identity or another run's.
function withRunKey(email: string, runKey: string): string {
  const at = email.indexOf("@");
  if (at === -1) return `${email}+${runKey}`;
  return `${email.slice(0, at)}+${runKey}${email.slice(at)}`;
}

const DEFAULTS = {
  verifiedEmail: "e2e-verified@scubed.com.bd",
  unverifiedEmail: "e2e-unverified@scubed.com.bd",
  verifiedPassword: "E2eFixture-Verified#2026",
  unverifiedPassword: "E2eFixture-Unverified#2026",
  invitationToken: "e2e-invitation-token-2026-fixture",
};

// Prefers the new-format secret key (SUPABASE_SECRET_KEYS, JSON-encoded,
// keyed by name) over the legacy SUPABASE_SERVICE_ROLE_KEY. Falls back to
// the legacy key on parse failure or when the new var is absent (local /
// self-hosted projects without signing-key migration).
function resolveServiceKey(): string | undefined {
  const raw = Deno.env.get("SUPABASE_SECRET_KEYS");
  if (raw) {
    try {
      const parsed = JSON.parse(raw);
      if (parsed?.default) return parsed.default;
    } catch {
      // fall through to legacy
    }
  }
  return Deno.env.get("SUPABASE_SERVICE_ROLE_KEY");
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

async function sha256Hex(input: string): Promise<string> {
  const bytes = new TextEncoder().encode(input);
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function randomPassword(): string {
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

async function ensureUser(
  admin: any,
  opts: {
    email: string;
    password: string;
    emailConfirm: boolean;
    fullName: string;
    appMetadata: Record<string, unknown>;
    accountIdHint: string;
    // Phase 19 tenant claim custom_access_token_hook reads off
    // auth.users.raw_user_meta_data->>'selected_tenant_id'. Optional: only
    // verifiedUser/unverifiedUser pass this, inviterUser doesn't sign in.
    selectedTenantId?: string;
  },
) {
  const userMetadata: Record<string, unknown> = { full_name: opts.fullName };
  if (opts.selectedTenantId) {
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
      },
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
  if (error) {
    if (error.status === 422 || error.status === 400) {
      const { data: list, error: listErr } = await admin.auth.admin.listUsers({
        page: 1,
        perPage: 1000,
      });
      if (listErr) throw new Error(`listUsers failed: ${listErr.message}`);
      const existing = list.users.find(
        (u: any) => u.email?.toLowerCase() === opts.email.toLowerCase(),
      );
      if (!existing) throw new Error(error.message);
      const { data: upd, error: updErr } =
        await admin.auth.admin.updateUserById(existing.id, {
          email: opts.email,
          password: opts.password,
          email_confirm: opts.emailConfirm,
          app_metadata: opts.appMetadata,
          user_metadata: userMetadata,
        });
      if (updErr) throw new Error(`updateUserById failed: ${updErr.message}`);
      return upd.user;
    }
    throw new Error(`createUser failed: ${error.message}`);
  }
  return data.user;
}

async function seedTenantsAndMemberships(
  admin: any,
  ids: typeof LOCAL_IDS,
  users: { verifiedUser: any; unverifiedUser: any },
) {
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
    { onConflict: "id" },
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
    { onConflict: "tenant_id,user_id" },
  );
  if (tenantUserErr) {
    throw new Error(`tenant_users upsert failed: ${tenantUserErr.message}`);
  }
}

async function seedAccountsAndMemberships(
  admin: any,
  ids: typeof LOCAL_IDS,
  users: { verifiedUser: any; unverifiedUser: any; inviterUser: any },
) {
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
    { onConflict: "id" },
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
  // moment later in a second, non-atomic Supabase call. Between those two
  // calls every one of those rows briefly had zero memberships. A read
  // landing in that window (this job's own, or a concurrent CI job's,
  // hitting these same fixed ids) saw an empty membership list and tripped
  // control-plane's EnsureViewerContext into provisionDefaultWorkspace,
  // handing the signed-in user a brand new, unrelated workspace instead of
  // the fixture's intended one. That is what produced auth-shell.spec.ts's
  // "members page redirects unverified users" failure: a genuine server
  // error, not stale data, from a page whose Promise.all had nothing to
  // catch it.
  const { error: clearErr } = await admin
    .from("account_memberships")
    .delete()
    .eq("account_id", ids.invitedAccountId)
    .eq("user_id", verifiedUser.id);
  if (clearErr) {
    throw new Error(
      `membership delete failed (${ids.invitedAccountId}/${verifiedUser.id}): ${clearErr.message}`,
    );
  }

  const { error: memErr } = await admin
    .from("account_memberships")
    .upsert(
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
      ],
      { onConflict: "account_id,user_id" },
    );
  if (memErr) throw new Error(`memberships upsert failed: ${memErr.message}`);
}

async function resetProfilesAndInvitation(
  admin: any,
  ids: typeof LOCAL_IDS,
  users: { verifiedUser: any; unverifiedUser: any; inviterUser: any },
  invitationTokenHash: string,
  invitationEmail: string,
  unverifiedEmail: string,
) {
  const { inviterUser } = users;

  const { error: profErr } = await admin.from("account_profiles").upsert(
    [
      {
        account_id: ids.verifiedPrimaryAccountId,
        owner_name: "E2E Verified Owner",
        login_email: invitationEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: ids.verifiedSecondaryAccountId,
        owner_name: "E2E Shared Owner",
        login_email: invitationEmail,
        country_code: null,
        state_region: null,
        profile_setup_complete: false,
      },
      {
        account_id: ids.invitedAccountId,
        owner_name: "E2E Inviter Owner",
        login_email: invitationEmail,
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
    { onConflict: "account_id" },
  );
  if (profErr) throw new Error(`profiles upsert failed: ${profErr.message}`);

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
        `billing profile delete failed (${accountId}): ${error.message}`,
      );
    }
  }

  // No delete before this upsert: onConflict on id already rewrites every
  // field (including accepted_at back to null) in one atomic statement, the
  // same fix as the account_memberships one above, closed here too since it
  // is the same file and the same shape of bug.
  const { error: upInvErr } = await admin
    .from("account_invitations")
    .upsert(
      [
        {
          id: ids.invitationId,
          account_id: ids.invitedAccountId,
          email: invitationEmail,
          role: "member",
          token_hash: invitationTokenHash,
          expires_at: "2099-01-01T00:00:00Z",
          accepted_at: null,
          invited_by_user_id: inviterUser.id,
        },
      ],
      { onConflict: "id" },
    );
  if (upInvErr) throw new Error(`invitation upsert failed: ${upInvErr.message}`);
}

// A CI job's run key (see buildIds) is only ever reused by retries of that
// exact job attempt, never by a later one, so any namespaced fixture row
// older than a job could plausibly still be running is abandoned. Swept on
// every reset call rather than on a schedule or a dedicated teardown step,
// so it needs no new CI wiring and runs from the one entrypoint every
// caller already goes through. Scoped to accounts with an "e2e-" slug AND a
// profile login_email containing "+" (only ever true for a run-key-derived
// email, see withRunKey): the slug rules out ever touching a real customer
// account, the email rules out the shared local/default identity, which has
// no run key and can be arbitrarily old.
const STALE_RUN_HOURS = 3;

async function sweepStaleFixtureRuns(admin: any): Promise<void> {
  const cutoffIso = new Date(Date.now() - STALE_RUN_HOURS * 3600 * 1000)
    .toISOString();

  // Both conditions matter: slug is the belt (every account this function
  // ever creates is prefixed "e2e-", nothing else in this project is), the
  // "+" email check on the profile join below is the suspenders (rules out
  // a real customer account that happens to also start with that prefix).
  // Sweeping on the email pattern alone would have deleted any real account
  // whose owner happens to use a "+" Gmail-style address, which is common
  // enough in the wild that this is not a hypothetical.
  const { data: oldAccounts, error: accErr } = await admin
    .from("accounts")
    .select("id, owner_user_id")
    .like("slug", "e2e-%")
    .lt("created_at", cutoffIso);
  if (accErr || !oldAccounts?.length) return;

  const { data: profiles, error: profErr } = await admin
    .from("account_profiles")
    .select("account_id")
    .in("account_id", oldAccounts.map((a: any) => a.id))
    .like("login_email", "%+%@%");
  if (profErr || !profiles?.length) return;

  const staleAccountIds = new Set(profiles.map((p: any) => p.account_id));
  const stale = oldAccounts.filter((a: any) => staleAccountIds.has(a.id));

  for (const acct of stale) {
    // accounts.id cascades to every account-scoped table (memberships,
    // profiles, billing, invitations, credits, api keys, ...). Deleting it
    // first is what makes the user delete below legal: accounts.owner_
    // user_id has no ON DELETE CASCADE, so a user who still owns an account
    // cannot be deleted.
    const { error: delAcctErr } = await admin
      .from("accounts")
      .delete()
      .eq("id", acct.id);
    if (delAcctErr) {
      console.error(`sweep: delete account ${acct.id} failed: ${delAcctErr.message}`);
      continue;
    }
    const { error: delUserErr } = await admin.auth.admin.deleteUser(
      acct.owner_user_id,
    );
    if (delUserErr) {
      console.error(
        `sweep: delete user ${acct.owner_user_id} failed: ${delUserErr.message}`,
      );
    }
  }
}

Deno.serve(async (req) => {
  if (req.method !== "POST") {
    return jsonResponse({ error: "method not allowed" }, 405);
  }

  // Auth: accept the dedicated E2E_FIXTURE_SECRET (when set), the legacy
  // auto-injected SUPABASE_SERVICE_ROLE_KEY, or the new-format resolved
  // service key (SUPABASE_SECRET_KEYS). Every caller in CI / local already
  // has one of these, so accepting them removes the separate secret-setup
  // step while keeping the endpoint locked to the same blast radius (root
  // DB access). Additive only — neither existing fallback is removed.
  const acceptedSecrets = [
    Deno.env.get("E2E_FIXTURE_SECRET"),
    Deno.env.get("SUPABASE_SERVICE_ROLE_KEY"),
    resolveServiceKey(),
  ].filter((v): v is string => !!v);
  if (acceptedSecrets.length === 0) {
    return jsonResponse(
      { error: "E2E_FIXTURE_SECRET / SUPABASE_SERVICE_ROLE_KEY not configured" },
      500,
    );
  }
  const provided = req.headers.get("X-E2E-Secret");
  if (!provided || !acceptedSecrets.includes(provided)) {
    return jsonResponse({ error: "unauthorized" }, 401);
  }

  let body: {
    action?: string;
    // runKey namespaces every id and email this call touches (see buildIds).
    // CI sets it to one value per job attempt; local/manual callers omit it
    // and get the original shared fixture identity, unchanged.
    runKey?: string;
    // Only meaningful alongside runKey: the emails and invitation token CI
    // already resolved for its Playwright processes (see e2e-auth-fixtures.
    // mjs), so this function seeds the exact identity the specs expect
    // instead of independently re-deriving the same string twice.
    verifiedEmail?: string;
    unverifiedEmail?: string;
    invitationToken?: string;
  } = {};
  try {
    body = await req.json();
  } catch {
    // empty body allowed — default to reset
  }
  const action = body.action ?? "reset";
  if (action !== "reset" && action !== "reset-profile") {
    return jsonResponse({ error: `unknown action: ${action}` }, 400);
  }

  const supabaseUrl = Deno.env.get("SUPABASE_URL");
  const serviceRoleKey = resolveServiceKey();
  if (!supabaseUrl || !serviceRoleKey) {
    return jsonResponse(
      { error: "SUPABASE_URL / SUPABASE_SERVICE_ROLE_KEY missing" },
      500,
    );
  }

  const admin = createClient(supabaseUrl, serviceRoleKey, {
    auth: { persistSession: false, autoRefreshToken: false },
  });

  if (action === "reset-profile") {
    const targetEmail: string =
      (body as { email?: string }).email ??
      (Deno.env.get("E2E_DEFAULT_VERIFIED_EMAIL") ?? DEFAULTS.verifiedEmail);

    // Look up the account via account_profiles.login_email directly.
    // Avoids auth.admin.listUsers({ page, perPage }) which triggers GoTrue
    // "Database error finding users" in Supabase edge functions.
    const { data: profileRow, error: profileLookupErr } = await admin
      .from("account_profiles")
      .select("account_id")
      .eq("login_email", targetEmail)
      .limit(1)
      .maybeSingle();
    if (profileLookupErr) {
      return jsonResponse(
        { error: `profile lookup failed: ${profileLookupErr.message}` },
        500,
      );
    }
    if (!profileRow) {
      return jsonResponse(
        { error: `user not found for email: ${targetEmail}` },
        404,
      );
    }

    const { data: membership, error: memErr } = await admin
      .from("account_memberships")
      .select("account_id")
      .eq("account_id", profileRow.account_id)
      .eq("role", "owner")
      .maybeSingle();
    if (memErr) {
      return jsonResponse(
        { error: `membership lookup failed: ${memErr.message}` },
        500,
      );
    }
    if (!membership) {
      return jsonResponse(
        { error: `no owner membership found for user: ${targetEmail}` },
        404,
      );
    }

    const { error: profErr } = await admin
      .from("account_profiles")
      .update({ profile_setup_complete: false })
      .eq("account_id", membership.account_id);
    if (profErr) {
      return jsonResponse(
        { error: `profile reset failed: ${profErr.message}` },
        500,
      );
    }

    return jsonResponse({ ok: true, email: targetEmail, account_id: membership.account_id });
  }

  const runKey = typeof body.runKey === "string" ? body.runKey.trim() : "";
  const ids = await buildIds(runKey);

  const verifiedEmail =
    body.verifiedEmail ??
    Deno.env.get("E2E_DEFAULT_VERIFIED_EMAIL") ??
    DEFAULTS.verifiedEmail;
  const unverifiedEmail =
    body.unverifiedEmail ??
    Deno.env.get("E2E_DEFAULT_UNVERIFIED_EMAIL") ??
    DEFAULTS.unverifiedEmail;
  const verifiedPassword =
    Deno.env.get("E2E_DEFAULT_VERIFIED_PASSWORD") ?? DEFAULTS.verifiedPassword;
  const unverifiedPassword =
    Deno.env.get("E2E_DEFAULT_UNVERIFIED_PASSWORD") ??
    DEFAULTS.unverifiedPassword;
  const invitationToken =
    body.invitationToken ??
    Deno.env.get("E2E_DEFAULT_INVITATION_TOKEN") ??
    DEFAULTS.invitationToken;

  try {
    await sweepStaleFixtureRuns(admin);

    const [verifiedUser, unverifiedUser, inviterUser] = await Promise.all([
      ensureUser(admin, {
        email: verifiedEmail,
        password: verifiedPassword,
        emailConfirm: true,
        appMetadata: { hive_email_verified: true },
        fullName: "E2E Verified Owner",
        accountIdHint: ids.verifiedPrimaryAccountId,
        selectedTenantId: ids.verifiedTenantId,
      }),
      ensureUser(admin, {
        email: unverifiedEmail,
        password: unverifiedPassword,
        emailConfirm: true,
        appMetadata: { hive_email_verified: false },
        fullName: "E2E Unverified Owner",
        accountIdHint: ids.unverifiedAccountId,
        selectedTenantId: ids.unverifiedTenantId,
      }),
      ensureUser(admin, {
        email: ids.inviterEmail,
        password: randomPassword(),
        emailConfirm: true,
        appMetadata: { hive_email_verified: true },
        fullName: "E2E Inviter Owner",
        accountIdHint: ids.verifiedSecondaryAccountId,
      }),
    ]);

    await seedAccountsAndMemberships(admin, ids, {
      verifiedUser,
      unverifiedUser,
      inviterUser,
    });
    await seedTenantsAndMemberships(admin, ids, { verifiedUser, unverifiedUser });
    await resetProfilesAndInvitation(
      admin,
      ids,
      { verifiedUser, unverifiedUser, inviterUser },
      await sha256Hex(invitationToken),
      verifiedEmail,
      unverifiedEmail,
    );

    return jsonResponse({
      verifiedEmail,
      unverifiedEmail,
      verifiedPassword,
      unverifiedPassword,
      invitationToken,
      verifiedUserId: verifiedUser.id,
      unverifiedUserId: unverifiedUser.id,
      inviterUserId: inviterUser.id,
      verifiedPrimaryAccountId: ids.verifiedPrimaryAccountId,
      verifiedSecondaryAccountId: ids.verifiedSecondaryAccountId,
      invitedAccountId: ids.invitedAccountId,
      unverifiedAccountId: ids.unverifiedAccountId,
    });
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return jsonResponse({ error: message }, 500);
  }
});
