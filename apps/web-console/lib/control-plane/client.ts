import { cache } from "react";
import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";
import {
  isCheckoutReturnState,
  type CheckoutReturnState,
} from "@/lib/payments/checkout-return";
import {
  parseKeyLimits,
  validateLimits,
  type KeyLimits,
  type KeyLimitsInput,
} from "@/lib/api-keys";

export interface ViewerAccount {
  id: string;
  display_name: string;
  slug: string;
  account_type: string;
  role: string;
}

export interface ViewerMembership {
  account_id: string;
  account_display_name: string;
  account_slug: string;
  display_name: string;
  role: string;
  status: string;
}

export interface ViewerUser {
  id: string;
  email: string;
  email_verified: boolean;
}

export interface Viewer {
  user: ViewerUser;
  current_account: ViewerAccount;
  memberships: ViewerMembership[];
  permissions: string[];
}

export interface AccountProfile {
  owner_name: string;
  login_email: string;
  display_name: string;
  account_type: string;
  country_code: string;
  state_region: string;
  profile_setup_complete: boolean;
}

// The not-yet-set-up shape: a fresh account with no profile row (control-plane
// 404s), and the fallback a page takes when the profile fetch itself fails.
// Same reasoning as the 404 case below — render the needs-setup state rather
// than crash.
export const EMPTY_ACCOUNT_PROFILE: AccountProfile = {
  owner_name: "",
  login_email: "",
  display_name: "",
  account_type: "",
  country_code: "",
  state_region: "",
  profile_setup_complete: false,
};

export interface UpdateAccountProfileInput {
  ownerName: string;
  loginEmail: string;
  accountName: string;
  accountType: string;
  countryCode: string;
  stateRegion: string;
}

export interface BillingProfile {
  billing_contact_name: string;
  billing_contact_email: string;
  legal_entity_name: string;
  legal_entity_type: string;
  business_registration_number: string;
  vat_number: string;
  tax_id_type: string;
  tax_id_value: string;
  country_code: string;
  state_region: string;
}

export interface UpdateBillingProfileInput {
  billingContactName: string;
  billingContactEmail: string;
  legalEntityName: string;
  legalEntityType: string;
  businessRegistrationNumber: string;
  vatNumber: string;
  taxIdType: string;
  taxIdValue: string;
  countryCode: string;
  stateRegion: string;
}

export interface AccountMember {
  user_id: string;
  // The member's login email, so the members table can show a human identity
  // instead of a raw UUID (issue #536). Empty when the upstream row has none.
  email: string;
  role: string;
  status: string;
}

interface ViewerResponse {
  user: ViewerUser;
  current_account: {
    id: string;
    display_name: string;
    account_type: string;
    role: string;
  };
  memberships: Array<{
    account_id: string;
    display_name: string;
    role: string;
    status: string;
  }>;
  permissions: string[];
}

type JsonPrimitive = string | number | boolean | null;
interface JsonObject {
  [key: string]: JsonValue;
}
type JsonArray = JsonValue[];
type JsonValue = JsonPrimitive | JsonObject | JsonArray;

interface RequestContext {
  baseUrl: string;
  headers: Record<string, string>;
}

// Memoized per request with React's cache(): confirmed benefit is scoped to
// the ~16 page.tsx/layout.tsx Server Components that call these exports (the
// budget settings page alone calls getViewer, getAccountProfile, and
// getBudget; its parent layout calls getViewer, getBalance, and
// getBudgetThreshold on the same navigation — up to 6 calls per page load,
// unmemoized). cache() dedup is scoped by React to the render of that
// component tree; it does not apply to the ~15 Route Handlers under
// app/api/**/route.ts that import this module, but each of those calls a
// getRequestContext-consuming function once per invocation, so there is no
// multi-call dedup to lose there either way.
//
// Each of those (up to 6, per Server Component render) calls was a real
// network round trip to Supabase Auth's getUser(), and a chance for a
// transient upstream hiccup to throw "No active session" — confirmed live: a
// CI run of tests/e2e/console-budgets.spec.ts failed with the console's
// generic error boundary ("Something went wrong on this page") instead of
// the budget page. cache() collapses those up-to-6 chances into 1 per
// request (scoped to that one request; reset on the next navigation, so this
// does not change session freshness). The one-retry below closes most of
// what's left of that one remaining chance. Neither eliminates it: a retry
// exhausted or a memoized rejection is still a thrown error, and the two
// call sites that throw uncaught on it (getViewer, getAccountProfile in
// app/console/billing/budget/page.tsx and its layout) now catch it and
// redirect to sign-in instead of letting it reach the generic boundary — see
// those two files. The other 16 console pages that call getViewer() directly
// still have the bare crash-on-throw path; known follow-up, not closed here.
const getRequestContext = cache(async (): Promise<RequestContext> => {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);

  // Validate the JWT against Supabase (getUser does a server round-trip
  // and rejects revoked tokens) before trusting the session. getSession
  // only reads the cookie and would accept a revoked token.
  //
  // One retry only: a transient upstream hiccup (the failure class that
  // crashed the budget page, see above) usually clears on a second attempt
  // a moment later. A genuinely revoked or invalid token fails identically
  // both times, so this cannot turn a real refusal into a false accept.
  let user: Awaited<ReturnType<typeof supabase.auth.getUser>>["data"]["user"] =
    null;
  let userError: Awaited<ReturnType<typeof supabase.auth.getUser>>["error"] =
    null;
  for (let attempt = 0; attempt < 2; attempt++) {
    const result = await supabase.auth.getUser();
    user = result.data.user;
    userError = result.error;
    if (!userError && user) break;
  }
  if (userError || !user) {
    throw new Error("No active session");
  }

  const {
    data: { session },
  } = await supabase.auth.getSession();

  if (!session) {
    throw new Error("No active session");
  }

  const baseUrl = process.env.CONTROL_PLANE_BASE_URL;
  if (!baseUrl) {
    throw new Error("CONTROL_PLANE_BASE_URL is not configured");
  }

  const headers: Record<string, string> = {
    Authorization: `Bearer ${session.access_token}`,
    "Content-Type": "application/json",
  };

  const accountId = cookieStore.get("hive_account_id")?.value;
  if (accountId) {
    headers["X-Hive-Account-ID"] = accountId;
  }

  return { baseUrl, headers };
});

function isJsonObject(value: JsonValue | null): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseJsonValue(text: string): JsonValue | null {
  if (!text) {
    return null;
  }

  try {
    const parsed: JsonValue = JSON.parse(text);
    return parsed;
  } catch {
    return null;
  }
}

function readStringField(source: JsonObject, key: string): string | null {
  const value = source[key];
  return typeof value === "string" ? value : null;
}

function readBooleanField(source: JsonObject, key: string): boolean | null {
  const value = source[key];
  return typeof value === "boolean" ? value : null;
}

function readObjectField(source: JsonObject, key: string): JsonObject | null {
  const value = source[key];
  return isJsonObject(value) ? value : null;
}

function readArrayField(source: JsonObject, key: string): JsonArray | null {
  const value = source[key];
  return Array.isArray(value) ? value : null;
}

// requireArrayField is readArrayField plus a loud failure mode for the
// analytics endpoints (issue #856). readArrayField collapses "key absent",
// "key wrong-typed", and "key present but genuinely empty" down to the same
// `null` versus `[]` split; the first two are a response-shape contract
// break (a backend rename, a proxy mangling a 200, a nested wrapper change),
// the third is a real, silent, zero-usage account. `?? []` used to treat
// all three identically, which is exactly how #856 (every analytics call
// silently parsed to empty regardless of what usage_events held) shipped
// undetected. A missing or wrong-typed key now throws instead of defaulting,
// so the next contract drift surfaces as the page's existing "Unable to
// load analytics" error state rather than a second silent all-zero read.
function requireArrayField(source: JsonObject, key: string, context: string): JsonArray {
  const value = readArrayField(source, key);
  if (value === null) {
    throw new Error(`${context}: expected "${key}" to be an array in the response`);
  }
  return value;
}

function readStringArrayField(source: JsonObject, key: string): string[] {
  const arr = readArrayField(source, key);
  if (!arr) return [];
  const result: string[] = [];
  for (const item of arr) {
    if (typeof item === "string") {
      result.push(item);
    }
  }
  return result;
}

function decodeViewerResponse(payload: JsonObject): ViewerResponse | null {
  const user = readObjectField(payload, "user");
  const currentAccount = readObjectField(payload, "current_account");
  const membershipsValue = readArrayField(payload, "memberships");

  if (!user || !currentAccount || !membershipsValue) {
    return null;
  }

  const userId = readStringField(user, "id");
  const userEmail = readStringField(user, "email");
  const userEmailVerified = readBooleanField(user, "email_verified");
  const currentAccountId = readStringField(currentAccount, "id");
  const currentAccountDisplayName = readStringField(currentAccount, "display_name");
  const currentAccountType = readStringField(currentAccount, "account_type");
  const currentAccountRole = readStringField(currentAccount, "role");
  const permissions = readStringArrayField(payload, "permissions");

  if (
    !userId ||
    !userEmail ||
    userEmailVerified === null ||
    !currentAccountId ||
    !currentAccountDisplayName ||
    !currentAccountType ||
    !currentAccountRole
  ) {
    return null;
  }

  const memberships: ViewerResponse["memberships"] = [];
  for (const membershipValue of membershipsValue) {
    if (!isJsonObject(membershipValue)) {
      return null;
    }

    const accountId = readStringField(membershipValue, "account_id");
    const displayName = readStringField(membershipValue, "display_name");
    const role = readStringField(membershipValue, "role");
    const status = readStringField(membershipValue, "status");

    if (!accountId || !displayName || !role || !status) {
      return null;
    }

    memberships.push({
      account_id: accountId,
      display_name: displayName,
      role,
      status,
    });
  }

  return {
    user: {
      id: userId,
      email: userEmail,
      email_verified: userEmailVerified,
    },
    current_account: {
      id: currentAccountId,
      display_name: currentAccountDisplayName,
      account_type: currentAccountType,
      role: currentAccountRole,
    },
    memberships,
    permissions,
  };
}

function decodeAccountProfile(payload: JsonObject): AccountProfile | null {
  const ownerName = readStringField(payload, "owner_name");
  const loginEmail = readStringField(payload, "login_email");
  const displayName = readStringField(payload, "display_name");
  const accountType = readStringField(payload, "account_type");
  const countryCode = readStringField(payload, "country_code");
  const stateRegion = readStringField(payload, "state_region");
  const profileSetupComplete = readBooleanField(payload, "profile_setup_complete");

  if (
    !ownerName ||
    !loginEmail ||
    !displayName ||
    !accountType ||
    countryCode === null ||
    stateRegion === null ||
    profileSetupComplete === null
  ) {
    return null;
  }

  return {
    owner_name: ownerName,
    login_email: loginEmail,
    display_name: displayName,
    account_type: accountType,
    country_code: countryCode,
    state_region: stateRegion,
    profile_setup_complete: profileSetupComplete,
  };
}

function decodeBillingProfile(payload: JsonObject): BillingProfile | null {
  const billingContactName = readStringField(payload, "billing_contact_name");
  const billingContactEmail = readStringField(payload, "billing_contact_email");
  const legalEntityName = readStringField(payload, "legal_entity_name");
  const legalEntityType = readStringField(payload, "legal_entity_type");
  const businessRegistrationNumber = readStringField(
    payload,
    "business_registration_number"
  );
  const vatNumber = readStringField(payload, "vat_number");
  const taxIdType = readStringField(payload, "tax_id_type");
  const taxIdValue = readStringField(payload, "tax_id_value");
  const countryCode = readStringField(payload, "country_code");
  const stateRegion = readStringField(payload, "state_region");

  if (
    billingContactName === null ||
    billingContactEmail === null ||
    legalEntityName === null ||
    !legalEntityType ||
    businessRegistrationNumber === null ||
    vatNumber === null ||
    taxIdType === null ||
    taxIdValue === null ||
    countryCode === null ||
    stateRegion === null
  ) {
    return null;
  }

  return {
    billing_contact_name: billingContactName,
    billing_contact_email: billingContactEmail,
    legal_entity_name: legalEntityName,
    legal_entity_type: legalEntityType,
    business_registration_number: businessRegistrationNumber,
    vat_number: vatNumber,
    tax_id_type: taxIdType,
    tax_id_value: taxIdValue,
    country_code: countryCode,
    state_region: stateRegion,
  };
}

function decodeMembers(payload: JsonObject): AccountMember[] {
  const membersValue = readArrayField(payload, "members");
  if (!membersValue) {
    return [];
  }

  const members: AccountMember[] = [];
  for (const memberValue of membersValue) {
    if (!isJsonObject(memberValue)) {
      continue;
    }

    const userId = readStringField(memberValue, "user_id");
    const role = readStringField(memberValue, "role");
    const status = readStringField(memberValue, "status");

    if (!userId || !role || !status) {
      continue;
    }

    members.push({
      user_id: userId,
      // A member with no email upstream still lists; the table renders plain
      // language for the missing identity rather than dropping the row.
      email: readStringField(memberValue, "email") ?? "",
      role,
      status,
    });
  }

  return members;
}

async function readResponseText(response: Response): Promise<string> {
  try {
    return await response.text();
  } catch {
    return "";
  }
}

function readErrorMessage(payload: JsonValue | null): string | null {
  if (!isJsonObject(payload)) {
    return null;
  }

  return readStringField(payload, "error");
}

async function readResponseError(response: Response, fallback: string): Promise<string> {
  const bodyText = await readResponseText(response);
  const payload = parseJsonValue(bodyText);

  return readErrorMessage(payload) ?? `${fallback}: ${response.status}`;
}

// ControlPlaneError carries the upstream HTTP status so Next.js proxy routes
// can forward it instead of collapsing every failure to 500. Use this for
// any client function whose proxy route must preserve 4xx semantics
// (permission denied, validation error, not found).
export class ControlPlaneError extends Error {
  public readonly status: number;
  // Stable machine code from the upstream body (e.g. "last_owner_required"),
  // when one is present. Status alone is ambiguous: several distinct refusals
  // share a status, and a proxy route needs the exact one to state the true
  // reason back to the customer.
  public readonly code: string | null;
  constructor(status: number, message: string, code: string | null = null) {
    super(message);
    this.name = "ControlPlaneError";
    this.status = status;
    this.code = code;
  }
}

async function throwControlPlaneError(response: Response, fallback: string): Promise<never> {
  const bodyText = await readResponseText(response);
  const payload = parseJsonValue(bodyText);
  const message = readErrorMessage(payload) ?? `${fallback}: ${response.status}`;
  const code = isJsonObject(payload) ? readStringField(payload, "code") : null;
  throw new ControlPlaneError(response.status, message, code);
}

export async function getViewer(): Promise<Viewer> {
  const { baseUrl, headers } = await getRequestContext();

  const response = await fetch(`${baseUrl}/api/v1/viewer`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch viewer"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse viewer response");
  }

  const rawViewer = decodeViewerResponse(payload);
  if (!rawViewer) {
    throw new Error("Failed to parse viewer response");
  }

  return {
    user: rawViewer.user,
    current_account: {
      id: rawViewer.current_account.id,
      display_name: rawViewer.current_account.display_name,
      slug: "",
      account_type: rawViewer.current_account.account_type,
      role: rawViewer.current_account.role,
    },
    memberships: rawViewer.memberships.map((membership) => ({
      account_id: membership.account_id,
      account_display_name: membership.display_name,
      account_slug: "",
      display_name: membership.display_name,
      role: membership.role,
      status: membership.status,
    })),
    permissions: Array.isArray(rawViewer.permissions) ? rawViewer.permissions : [],
  };
}

// TenantProvisionStatus is the outcome of reconciling the signed-in user
// against the tenant scope. "provisioned" means they now hold an active tenant
// membership, either because one already existed or because the control-plane
// just created it. "no_tenant" means no tenant matched them and an
// administrator has to invite them.
export type TenantProvisionStatus = "provisioned" | "no_tenant";

// reconcileTenantMembership asks the control-plane to settle the signed-in
// user's tenant membership. The user is derived server-side from the validated
// bearer token, never from a request body, so this cannot be pointed at
// somebody else's account.
//
// Any failure resolves to "no_tenant" rather than throwing: the caller is a
// layout, and the designed no-workspace state is a far better outcome there
// than an unhandled error collapsing the whole Server Components tree.
export async function reconcileTenantMembership(): Promise<TenantProvisionStatus> {
  const { baseUrl, headers } = await getRequestContext();

  const response = await fetch(`${baseUrl}/api/v1/viewer/tenant-provision`, {
    method: "POST",
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    return "no_tenant";
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    return "no_tenant";
  }

  const status = readStringField(payload, "status");
  return status === "provisioned" ? "provisioned" : "no_tenant";
}

// FeatureGate is one row of the admin feature-gate table (issue #292): a
// gate key, its human label + category, and whether it is enabled for the
// current tenant.
export interface FeatureGate {
  key: string;
  label: string;
  category: string;
  enabled: boolean;
  // manageable is false for a gate this caller may read but not change:
  // billing entitlements and deployment-shape gates stay platform-admin only
  // (issue #758). The UI renders those rows read-only rather than offering a
  // toggle the API would refuse.
  manageable: boolean;
}

// FeatureGates is the control-plane response for the admin feature-gate list.
// Gates are scoped server-side to the caller's selected tenant; the tenant id
// is deliberately not echoed on the wire.
export interface FeatureGates {
  gates: FeatureGate[];
}

function decodeFeatureGates(payload: JsonObject): FeatureGates | null {
  const gatesValue = readArrayField(payload, "gates");
  if (gatesValue === null) {
    return null;
  }

  const gates: FeatureGate[] = [];
  for (const item of gatesValue) {
    if (!isJsonObject(item)) {
      return null;
    }
    const key = readStringField(item, "key");
    const label = readStringField(item, "label");
    const category = readStringField(item, "category");
    const enabled = readBooleanField(item, "enabled");
    if (key === null || label === null || category === null || enabled === null) {
      return null;
    }
    // A control-plane that predates issue #758 omits manageable. Treating that
    // as manageable keeps the pre-#758 behaviour during a rolling deploy: the
    // API is the authority and refuses what it must.
    const manageable = readBooleanField(item, "manageable") ?? true;
    gates.push({ key, label, category, enabled, manageable });
  }

  return { gates };
}

// getFeatureGates lists every registered feature gate joined with the current
// tenant's enablement. Server-only (uses the session bearer); the control-plane
// gates this on the workspace administrator (the OWNER of the tenant in scope,
// or a platform admin) and returns 403 otherwise, surfaced here as a
// ControlPlaneError so the caller can render an access state.
export async function getFeatureGates(): Promise<FeatureGates> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/admin/feature-gates`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to load feature gates");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse feature gates response");
  }

  const decoded = decodeFeatureGates(payload);
  if (!decoded) {
    throw new Error("Failed to parse feature gates response");
  }

  return decoded;
}

// setFeatureGate toggles a single gate for the current tenant. Server-only;
// throws ControlPlaneError so a Route Handler can map the upstream status to a
// customer-safe message.
export async function setFeatureGate(
  key: string,
  enabled: boolean,
): Promise<{ key: string; enabled: boolean }> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(
    `${baseUrl}/api/v1/admin/feature-gates/${encodeURIComponent(key)}`,
    {
      method: "PUT",
      headers,
      cache: "no-store",
      body: JSON.stringify({ enabled }),
    },
  );

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to update feature gate");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse feature gate response");
  }

  const outKey = readStringField(payload, "key");
  const outEnabled = readBooleanField(payload, "enabled");
  if (outKey === null || outEnabled === null) {
    throw new Error("Failed to parse feature gate response");
  }

  return { key: outKey, enabled: outEnabled };
}

// MarketplaceEntryConfig is the kind-specific config blob for a marketplace
// catalog entry (issue #309): an MCP server's command/args/env or
// url/transport fields, or a free-form rule/skill/prompt-template body.
export type MarketplaceEntryConfig = JsonObject;

// MarketplaceEntry is one row of the admin-curated MCP and skills
// marketplace catalog, joined with this tenant's enablement.
export interface MarketplaceEntry {
  id: string;
  kind: string;
  name: string;
  description: string;
  config: MarketplaceEntryConfig;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface MarketplaceEntries {
  entries: MarketplaceEntry[];
  // canCurate is false for a workspace owner: the catalog is one global table
  // shared by every tenant, so adding, editing and removing an entry stays a
  // platform operation (issue #758). Enabling an entry for this workspace is
  // the part the owner controls.
  canCurate: boolean;
}

export interface CreateMarketplaceEntryInput {
  kind: string;
  name: string;
  description: string;
  config: MarketplaceEntryConfig;
}

export interface UpdateMarketplaceEntryInput {
  name: string;
  description: string;
  config: MarketplaceEntryConfig;
}

function decodeMarketplaceEntry(value: JsonValue | null): MarketplaceEntry | null {
  if (!isJsonObject(value)) {
    return null;
  }
  const id = readStringField(value, "id");
  const kind = readStringField(value, "kind");
  const name = readStringField(value, "name");
  const description = readStringField(value, "description");
  // A caller who may not curate the catalogue receives no config at all: it is
  // the raw configuration of a global catalogue row and an MCP entry can carry
  // a credential in its env, so the control-plane withholds it (security review
  // of PR #788). An absent key is a withheld value, not a malformed row; a
  // present key still has to be an object. Nothing here renders config for a
  // non-curator, so the empty default costs nothing.
  const config = "config" in value ? readObjectField(value, "config") : {};
  const enabled = readBooleanField(value, "enabled");
  const createdAt = readStringField(value, "created_at");
  const updatedAt = readStringField(value, "updated_at");
  if (
    id === null ||
    kind === null ||
    name === null ||
    description === null ||
    config === null ||
    enabled === null ||
    createdAt === null ||
    updatedAt === null
  ) {
    return null;
  }
  return {
    id,
    kind,
    name,
    description,
    config,
    enabled,
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

function decodeMarketplaceEntries(payload: JsonObject): MarketplaceEntries | null {
  const entriesValue = readArrayField(payload, "entries");
  if (entriesValue === null) {
    return null;
  }
  const entries: MarketplaceEntry[] = [];
  for (const item of entriesValue) {
    const decoded = decodeMarketplaceEntry(item);
    if (!decoded) {
      return null;
    }
    entries.push(decoded);
  }
  // A control-plane that predates issue #758 omits can_curate. Treating that as
  // curatable keeps the pre-#758 behaviour during a rolling deploy: the API is
  // the authority and refuses what it must.
  const canCurate = readBooleanField(payload, "can_curate") ?? true;
  return { entries, canCurate };
}

// getMarketplaceEntries lists the full catalog joined with this tenant's
// enablement (issue #309). Server-only; the control-plane gates this on the
// workspace administrator and returns 403 otherwise, surfaced as a
// ControlPlaneError so the caller can render an access state.
export async function getMarketplaceEntries(): Promise<MarketplaceEntries> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/admin/marketplace`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to load marketplace catalog");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse marketplace catalog response");
  }

  const decoded = decodeMarketplaceEntries(payload);
  if (!decoded) {
    throw new Error("Failed to parse marketplace catalog response");
  }
  return decoded;
}

// createMarketplaceEntry curates a new catalog entry. Server-only; throws
// ControlPlaneError so a Route Handler can map the upstream status to a
// customer-safe message.
export async function createMarketplaceEntry(
  input: CreateMarketplaceEntryInput,
): Promise<MarketplaceEntry> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/admin/marketplace`, {
    method: "POST",
    headers,
    cache: "no-store",
    body: JSON.stringify(input),
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to create marketplace entry");
  }

  const payload = parseJsonValue(await readResponseText(response));
  const decoded = decodeMarketplaceEntry(payload);
  if (!decoded) {
    throw new Error("Failed to parse marketplace entry response");
  }
  return decoded;
}

// updateMarketplaceEntry edits an existing catalog entry's mutable fields.
export async function updateMarketplaceEntry(
  id: string,
  input: UpdateMarketplaceEntryInput,
): Promise<MarketplaceEntry> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(
    `${baseUrl}/api/v1/admin/marketplace/${encodeURIComponent(id)}`,
    {
      method: "PUT",
      headers,
      cache: "no-store",
      body: JSON.stringify(input),
    },
  );

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to update marketplace entry");
  }

  const payload = parseJsonValue(await readResponseText(response));
  const decoded = decodeMarketplaceEntry(payload);
  if (!decoded) {
    throw new Error("Failed to parse marketplace entry response");
  }
  return decoded;
}

// deleteMarketplaceEntry removes a catalog entry. Every tenant's enablement
// of it is removed too (ON DELETE CASCADE).
export async function deleteMarketplaceEntry(id: string): Promise<void> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(
    `${baseUrl}/api/v1/admin/marketplace/${encodeURIComponent(id)}`,
    {
      method: "DELETE",
      headers,
      cache: "no-store",
    },
  );

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to delete marketplace entry");
  }
}

// setMarketplaceEntryEnabled enables or disables one catalog entry for the
// current tenant.
export async function setMarketplaceEntryEnabled(
  id: string,
  enabled: boolean,
): Promise<{ id: string; enabled: boolean }> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(
    `${baseUrl}/api/v1/admin/marketplace/${encodeURIComponent(id)}/enable`,
    {
      method: "PUT",
      headers,
      cache: "no-store",
      body: JSON.stringify({ enabled }),
    },
  );

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to update marketplace entry");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse marketplace entry response");
  }
  const outId = readStringField(payload, "id");
  const outEnabled = readBooleanField(payload, "enabled");
  if (outId === null || outEnabled === null) {
    throw new Error("Failed to parse marketplace entry response");
  }
  return { id: outId, enabled: outEnabled };
}

export async function getAccountProfile(): Promise<AccountProfile> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/profile`, {
    headers,
    cache: "no-store",
  });

  // Fresh accounts have no profile row yet — control-plane returns 404.
  // Surface that as an empty, not-yet-set-up profile so dashboard, setup,
  // and billing pages can render their needs-setup state instead of
  // crashing the whole Server Components tree.
  if (response.status === 404) {
    return EMPTY_ACCOUNT_PROFILE;
  }

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch account profile"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse account profile response");
  }

  const profile = decodeAccountProfile(payload);
  if (!profile) {
    throw new Error("Failed to parse account profile response");
  }

  return profile;
}

export async function updateAccountProfile(
  input: UpdateAccountProfileInput
): Promise<AccountProfile> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/profile`, {
    method: "PUT",
    headers,
    cache: "no-store",
    body: JSON.stringify({
      owner_name: input.ownerName,
      login_email: input.loginEmail,
      display_name: input.accountName,
      account_type: input.accountType,
      country_code: input.countryCode,
      state_region: input.stateRegion,
    }),
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to update account profile"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse account profile response");
  }

  const profile = decodeAccountProfile(payload);
  if (!profile) {
    throw new Error("Failed to parse account profile response");
  }

  return profile;
}

// createInvitation sends a workspace invite for the given email. It runs only
// server-side (Route Handler) so the internal CONTROL_PLANE_BASE_URL is never
// rendered into client HTML (issue #111). Throws ControlPlaneError so the route
// can map the upstream status (403 no-permission, 409 already-member, etc.) to a
// generic, customer-safe message instead of collapsing everything to 500.
//
// The control-plane returns the raw acceptance token in its 201 body. We return
// it here so a server-side caller (e.g. an invite mailer) can use it, but it is
// deliberately NOT surfaced in any client-facing redirect/URL — the token is
// bearer-equivalent and must not leak into browser history or logs.
export async function createInvitation(
  email: string,
  role: string,
): Promise<{ token: string | null }> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/invitations`, {
    method: "POST",
    headers,
    cache: "no-store",
    body: JSON.stringify({ email, role }),
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to send invitation");
  }

  const payload = parseJsonValue(await readResponseText(response));
  const token = isJsonObject(payload) ? readStringField(payload, "token") : null;
  return { token };
}

// updateMemberRole changes an existing member's workspace role. Server-side only
// (Route Handler) so CONTROL_PLANE_BASE_URL stays off the client, and the user's
// session bearer travels with the request. The control-plane makes the whole
// authorization decision, including the no-self-change and last-owner
// invariants; this helper only forwards the request and surfaces the refusal
// (status plus machine code) through ControlPlaneError.
export async function updateMemberRole(userId: string, role: string): Promise<void> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(
    `${baseUrl}/api/v1/accounts/current/members/${encodeURIComponent(userId)}`,
    {
      method: "PATCH",
      headers,
      cache: "no-store",
      body: JSON.stringify({ role }),
    },
  );

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to update the member role");
  }
}

export async function getBillingProfile(): Promise<BillingProfile> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/billing-profile`, {
    headers,
    cache: "no-store",
  });

  // Same posture as getAccountProfile above: an account with nothing stored
  // yet is a 404, not an outage. Surface it as an empty, not-yet-set-up
  // billing profile so the billing settings page renders its blank form
  // instead of crashing the Server Components tree. Any other failure still
  // throws so real outages stay visible.
  if (response.status === 404) {
    return {
      billing_contact_name: "",
      billing_contact_email: "",
      legal_entity_name: "",
      legal_entity_type: "",
      business_registration_number: "",
      vat_number: "",
      tax_id_type: "",
      tax_id_value: "",
      country_code: "",
      state_region: "",
    };
  }

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch billing profile"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse billing profile response");
  }

  const profile = decodeBillingProfile(payload);
  if (!profile) {
    throw new Error("Failed to parse billing profile response");
  }

  return profile;
}

export async function updateBillingProfile(
  input: UpdateBillingProfileInput
): Promise<BillingProfile> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/billing-profile`, {
    method: "PUT",
    headers,
    cache: "no-store",
    body: JSON.stringify({
      billing_contact_name: input.billingContactName,
      billing_contact_email: input.billingContactEmail,
      legal_entity_name: input.legalEntityName,
      legal_entity_type: input.legalEntityType,
      business_registration_number: input.businessRegistrationNumber,
      vat_number: input.vatNumber,
      tax_id_type: input.taxIdType,
      tax_id_value: input.taxIdValue,
      country_code: input.countryCode,
      state_region: input.stateRegion,
    }),
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to update billing profile"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse billing profile response");
  }

  const profile = decodeBillingProfile(payload);
  if (!profile) {
    throw new Error("Failed to parse billing profile response");
  }

  return profile;
}

export interface BalanceSummary {
  posted_credits: number;
  reserved_credits: number;
  available_credits: number;
}

export interface LedgerEntry {
  id: string;
  entry_type: string;
  credits_delta: number;
  idempotency_key: string;
  request_id: string;
  metadata: Record<string, unknown>;
  created_at: string;
}

export interface LedgerPage {
  entries: LedgerEntry[];
  next_cursor: string | null;
}

export interface Invoice {
  id: string;
  invoice_number: string;
  status: string;
  credits: number;
  // PHASE-17-OWNER-ONLY: The control-plane response carries a USD field for
  // internal accounting; this customer-surface interface intentionally omits
  // it. BD accounts must never see USD or any FX conversion language
  // (Phase 13 CONSOLE-13-04). Phase 17 strips the field at source — see
  // HANDOFF-13-03 in 13-AUDIT.md.
  amount_local: number;
  local_currency: string;
  tax_treatment: string;
  rail: string;
  line_items: Array<Record<string, unknown>>;
  created_at: string;
}

export interface CheckoutRail {
  rail: string;
  currency: string;
  label: string;
  enabled: boolean;
}

export interface CheckoutOptions {
  rails: CheckoutRail[];
  credit_increment: number;
  min_credits: number;
  max_credits: number;
  // Per-country pricing primitive in minor units of the resolved currency
  // (paisa for BDT, cents for USD), priced per `credit_block_size` credits.
  // Mirrors the control-plane Go int64 wire field. Replaces the FX-leaking
  // legacy primitive removed in Phase 17 / FX-17-03..04 (regulatory).
  // Display total: floor(credits * price_per_block_minor / credit_block_size).
  price_per_block_minor: number;
  credit_block_size: number;
  currency: string;
}

// CheckoutIntent is the customer-safe projection of one payment intent, used by
// the browser return page (issue #538).
//
// BD regulatory rule: this is a customer-visible surface, so the control-plane
// DTO carries no USD amount, no FX rate, and no currency-exchange language.
// Credits are the only quantity, and credits are currency free.
export interface CheckoutIntent {
  payment_intent_id: string;
  rail: string;
  status: string;
  state: CheckoutReturnState;
  credits: number;
}

export interface CheckoutInitiateResponse {
  payment_intent_id: string;
  redirect_url: string;
  rail: string;
  credits: number;
  amount_local: number;
  local_currency: string;
}

export interface ApiKey {
  id: string;
  nickname: string;
  status: string;
  redacted_suffix: string;
  created_at: string;
  updated_at: string;
  expires_at: string | null;
  last_used_at: string | null;
  expiration_summary: { kind: string; label: string };
  budget_summary: { kind: string; label: string };
  allowlist_summary: { mode: string; group_names: string[]; label: string };
  secret?: string;
}

export interface CatalogModel {
  id: string;
  display_name: string;
  summary: string;
  capability_badges: string[];
  pricing: {
    // Null, not zero, when the alias is priced from actual upstream cost:
    // there genuinely is no per-million price to show. Zero would render as
    // "free", which is both wrong and the most expensive kind of wrong on a
    // billing surface.
    input_price_credits: number | null;
    output_price_credits: number | null;
    cache_read_price_credits: number | null;
    cache_write_price_credits: number | null;
    pricing_mode: string;
  };
  lifecycle: string;
}

function readNumberField(source: JsonObject, key: string): number | null {
  const value = source[key];
  return typeof value === "number" ? value : null;
}

function decodeLedgerEntry(value: JsonValue): LedgerEntry | null {
  if (!isJsonObject(value)) {
    return null;
  }

  const id = readStringField(value, "id");
  const entryType = readStringField(value, "entry_type");
  const creditsDelta = readNumberField(value, "credits_delta");
  const idempotencyKey = readStringField(value, "idempotency_key") ?? "";
  const requestId = readStringField(value, "request_id") ?? "";
  const createdAt = readStringField(value, "created_at");

  if (!id || !entryType || creditsDelta === null || !createdAt) {
    return null;
  }

  const rawMetadata = readObjectField(value, "metadata");
  const metadata: Record<string, unknown> = {};
  if (rawMetadata) {
    for (const [k, v] of Object.entries(rawMetadata)) {
      metadata[k] = v;
    }
  }

  return {
    id,
    entry_type: entryType,
    credits_delta: creditsDelta,
    idempotency_key: idempotencyKey,
    request_id: requestId,
    metadata,
    created_at: createdAt,
  };
}

function decodeInvoice(value: JsonValue): Invoice | null {
  if (!isJsonObject(value)) {
    return null;
  }

  const id = readStringField(value, "id");
  const invoiceNumber = readStringField(value, "invoice_number") ?? "";
  const status = readStringField(value, "status") ?? "";
  const credits = readNumberField(value, "credits") ?? 0;
  // PHASE-17-OWNER-ONLY: drop the USD accounting field at the client
  // boundary — never reaches the customer-facing Invoice surface.
  // Hand-off to Phase 17 to remove on the wire (HANDOFF-13-03).
  const amountLocal = readNumberField(value, "amount_local") ?? 0;
  // CONSOLE-13-04 regulatory: never default to "USD". A missing
  // local_currency is a decode failure — propagate null so the customer
  // surface never silently inherits a USD label.
  const localCurrency = readStringField(value, "local_currency");
  const taxTreatment = readStringField(value, "tax_treatment") ?? "";
  const rail = readStringField(value, "rail") ?? "";
  const createdAt = readStringField(value, "created_at");

  if (!id || !createdAt || !localCurrency) {
    return null;
  }

  const rawLineItems = readArrayField(value, "line_items");
  const lineItems: Array<Record<string, unknown>> = [];
  if (rawLineItems) {
    for (const item of rawLineItems) {
      if (isJsonObject(item)) {
        const entry: Record<string, unknown> = {};
        for (const [k, v] of Object.entries(item)) {
          entry[k] = v;
        }
        lineItems.push(entry);
      }
    }
  }

  return {
    id,
    invoice_number: invoiceNumber,
    status,
    credits,
    amount_local: amountLocal,
    local_currency: localCurrency,
    tax_treatment: taxTreatment,
    rail,
    line_items: lineItems,
    created_at: createdAt,
  };
}

function decodeCheckoutRail(value: JsonValue): CheckoutRail | null {
  if (!isJsonObject(value)) {
    return null;
  }

  const rail = readStringField(value, "rail");
  const currency = readStringField(value, "currency");
  const label = readStringField(value, "label");
  const enabled = readBooleanField(value, "enabled");

  if (!rail || !currency || !label || enabled === null) {
    return null;
  }

  return { rail, currency, label, enabled };
}

function decodeApiKey(value: JsonValue): ApiKey | null {
  if (!isJsonObject(value)) {
    return null;
  }

  const id = readStringField(value, "id");
  const nickname = readStringField(value, "nickname") ?? "";
  const status = readStringField(value, "status") ?? "";
  const redactedSuffix = readStringField(value, "redacted_suffix") ?? "";
  const createdAt = readStringField(value, "created_at") ?? "";
  const updatedAt = readStringField(value, "updated_at") ?? "";
  const expiresAt = readStringField(value, "expires_at");
  const lastUsedAt = readStringField(value, "last_used_at");
  const secret = readStringField(value, "secret");

  if (!id) {
    return null;
  }

  const rawExpiration = readObjectField(value, "expiration_summary");
  const expirationSummary = rawExpiration
    ? {
        kind: readStringField(rawExpiration, "kind") ?? "",
        label: readStringField(rawExpiration, "label") ?? "",
      }
    : { kind: "", label: "" };

  const rawBudget = readObjectField(value, "budget_summary");
  const budgetSummary = rawBudget
    ? {
        kind: readStringField(rawBudget, "kind") ?? "",
        label: readStringField(rawBudget, "label") ?? "",
      }
    : { kind: "", label: "" };

  const rawAllowlist = readObjectField(value, "allowlist_summary");
  const rawGroupNames = rawAllowlist ? readArrayField(rawAllowlist, "group_names") : null;
  const groupNames: string[] = [];
  if (rawGroupNames) {
    for (const gn of rawGroupNames) {
      if (typeof gn === "string") {
        groupNames.push(gn);
      }
    }
  }
  const allowlistSummary = rawAllowlist
    ? {
        mode: readStringField(rawAllowlist, "mode") ?? "",
        group_names: groupNames,
        label: readStringField(rawAllowlist, "label") ?? "",
      }
    : { mode: "", group_names: [], label: "" };

  const key: ApiKey = {
    id,
    nickname,
    status,
    redacted_suffix: redactedSuffix,
    created_at: createdAt,
    updated_at: updatedAt,
    expires_at: expiresAt,
    last_used_at: lastUsedAt,
    expiration_summary: expirationSummary,
    budget_summary: budgetSummary,
    allowlist_summary: allowlistSummary,
  };

  if (secret !== null) {
    key.secret = secret;
  }

  return key;
}

function decodeCatalogModel(value: JsonValue): CatalogModel | null {
  if (!isJsonObject(value)) {
    return null;
  }

  const id = readStringField(value, "id");
  const displayName = readStringField(value, "display_name") ?? "";
  const summary = readStringField(value, "summary") ?? "";
  const lifecycle = readStringField(value, "lifecycle") ?? "active";

  if (!id) {
    return null;
  }

  const rawBadges = readArrayField(value, "capability_badges");
  const capabilityBadges: string[] = [];
  if (rawBadges) {
    for (const badge of rawBadges) {
      if (typeof badge === "string") {
        capabilityBadges.push(badge);
      }
    }
  }

  const rawPricing = readObjectField(value, "pricing");
  // No `?? 0` here. A variable-price alias sends null for both, and coercing
  // that to zero told the admin catalog the model was free.
  const inputPrice = rawPricing ? readNumberField(rawPricing, "input_price_credits") : null;
  const outputPrice = rawPricing ? readNumberField(rawPricing, "output_price_credits") : null;
  const pricingMode = rawPricing ? readStringField(rawPricing, "pricing_mode") ?? "fixed" : "fixed";
  const cacheReadPrice = rawPricing ? readNumberField(rawPricing, "cache_read_price_credits") : null;
  const cacheWritePrice = rawPricing ? readNumberField(rawPricing, "cache_write_price_credits") : null;

  return {
    id,
    display_name: displayName,
    summary,
    capability_badges: capabilityBadges,
    pricing: {
      input_price_credits: inputPrice,
      output_price_credits: outputPrice,
      cache_read_price_credits: cacheReadPrice,
      cache_write_price_credits: cacheWritePrice,
      pricing_mode: pricingMode,
    },
    lifecycle,
  };
}

export async function getBalance(): Promise<BalanceSummary> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/credits/balance`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch balance"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse balance response");
  }

  const postedCredits = readNumberField(payload, "posted_credits") ?? 0;
  const reservedCredits = readNumberField(payload, "reserved_credits") ?? 0;
  const availableCredits = readNumberField(payload, "available_credits") ?? 0;

  return {
    posted_credits: postedCredits,
    reserved_credits: reservedCredits,
    available_credits: availableCredits,
  };
}

export async function getLedgerEntries(params: {
  limit?: number;
  cursor?: string;
  type?: string;
  // Narrows the page to one request's reservation lifecycle (hold, charge,
  // release entries sharing that request_id) for the request-log detail view.
  requestId?: string;
}): Promise<LedgerPage> {
  const { baseUrl, headers } = await getRequestContext();

  const searchParams = new URLSearchParams();
  if (params.limit !== undefined) {
    searchParams.set("limit", String(params.limit));
  }
  if (params.cursor) {
    searchParams.set("cursor", params.cursor);
  }
  if (params.type) {
    searchParams.set("type", params.type);
  }
  if (params.requestId) {
    searchParams.set("request_id", params.requestId);
  }

  const qs = searchParams.toString();
  const url = `${baseUrl}/api/v1/accounts/current/credits/ledger${qs ? `?${qs}` : ""}`;

  const response = await fetch(url, { headers, cache: "no-store" });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch ledger entries"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse ledger response");
  }

  const rawEntries = readArrayField(payload, "entries") ?? [];
  const entries: LedgerEntry[] = [];
  for (const entry of rawEntries) {
    const decoded = decodeLedgerEntry(entry);
    if (decoded) {
      entries.push(decoded);
    }
  }

  const nextCursor = readStringField(payload, "next_cursor");

  return {
    entries,
    next_cursor: nextCursor,
  };
}

export async function getInvoices(): Promise<Invoice[]> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/invoices`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch invoices"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse invoices response");
  }

  const rawInvoices = readArrayField(payload, "invoices") ?? [];
  const invoices: Invoice[] = [];
  for (const item of rawInvoices) {
    const decoded = decodeInvoice(item);
    if (decoded) {
      invoices.push(decoded);
    }
  }

  return invoices;
}

export async function getInvoice(id: string): Promise<Invoice> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/invoices/${id}`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch invoice"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse invoice response");
  }

  const invoice = decodeInvoice(payload);
  if (!invoice) {
    throw new Error("Failed to parse invoice response");
  }

  return invoice;
}

export async function getCheckoutRails(): Promise<CheckoutOptions> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/checkout/rails`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to fetch checkout rails");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse checkout rails response");
  }

  const rawRails = readArrayField(payload, "rails") ?? [];
  const rails: CheckoutRail[] = [];
  for (const item of rawRails) {
    const decoded = decodeCheckoutRail(item);
    if (decoded) {
      rails.push(decoded);
    }
  }

  // Fallbacks are one-cent steps at the current credit unit (1 USD = 1e9
  // credits since the 2026-08-23 rescale); the server normally supplies all
  // three.
  const creditIncrement = readNumberField(payload, "credit_increment") ?? 10_000_000;
  const minCredits = readNumberField(payload, "min_credits") ?? 10_000_000;
  const maxCredits = readNumberField(payload, "max_credits") ?? 1_000_000_000;
  // FX-17-04 regulatory: pricing primitive must be in minor units of a
  // declared currency, priced per `credit_block_size` credits. Reject
  // payload without these fields rather than defaulting to a USD
  // assumption (mirrors the local_currency check in initiateCheckout).
  const pricePerBlockMinor = readNumberField(payload, "price_per_block_minor");
  const creditBlockSize = readNumberField(payload, "credit_block_size");
  const currency = readStringField(payload, "currency");
  // FX-17 review-pass: reject NaN / Infinity in addition to null / non-positive.
  // `readNumberField` only checks `typeof === "number"`, which is true for NaN,
  // and `creditBlockSize <= 0` is false for NaN — so without isFinite() a
  // pathological payload could reach the modal and render as NaN currency.
  if (
    pricePerBlockMinor === null ||
    !Number.isFinite(pricePerBlockMinor) ||
    creditBlockSize === null ||
    !Number.isFinite(creditBlockSize) ||
    creditBlockSize <= 0 ||
    !currency
  ) {
    throw new Error("Failed to parse checkout rails response");
  }
  return {
    rails,
    credit_increment: creditIncrement,
    min_credits: minCredits,
    max_credits: maxCredits,
    price_per_block_minor: pricePerBlockMinor,
    credit_block_size: creditBlockSize,
    currency,
  };
}

export async function initiateCheckout(
  rail: string,
  credits: number,
  idempotencyKey: string
): Promise<CheckoutInitiateResponse> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/checkout/initiate`, {
    method: "POST",
    headers,
    cache: "no-store",
    body: JSON.stringify({ rail, credits, idempotency_key: idempotencyKey }),
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to initiate checkout");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse checkout response");
  }

  const paymentIntentId = readStringField(payload, "payment_intent_id") ?? "";
  const redirectUrl = readStringField(payload, "redirect_url") ?? "";
  const responsRail = readStringField(payload, "rail") ?? rail;
  const responseCredits = readNumberField(payload, "credits") ?? credits;
  const amountLocal = readNumberField(payload, "amount_local") ?? 0;
  // CONSOLE-13-04 regulatory: never default to "USD". Treat a missing
  // local_currency as a decode failure rather than silently labelling the
  // checkout response in USD.
  const localCurrency = readStringField(payload, "local_currency");
  if (!localCurrency) {
    throw new Error("Failed to parse checkout response");
  }

  return {
    payment_intent_id: paymentIntentId,
    redirect_url: redirectUrl,
    rail: responsRail,
    credits: responseCredits,
    amount_local: amountLocal,
    local_currency: localCurrency,
  };
}

// getCheckoutIntent reads the authoritative state of one of the caller's own
// payment intents. The control-plane scopes the lookup to the viewer's account
// and reports another account's intent as not found, so a customer who edits the
// intent id in a return URL learns nothing and changes nothing.
//
// This is the only source of truth the return page uses. Query parameters on the
// return URL are attacker-controlled and are never treated as state.
export async function getCheckoutIntent(paymentIntentId: string): Promise<CheckoutIntent> {
  const { baseUrl, headers } = await getRequestContext();
  const query = new URLSearchParams({ payment_intent_id: paymentIntentId });
  const response = await fetch(
    `${baseUrl}/api/v1/accounts/current/checkout/intent?${query.toString()}`,
    { headers, cache: "no-store" }
  );

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to fetch payment status");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse payment status response");
  }

  const paymentIntent = readStringField(payload, "payment_intent_id");
  const rail = readStringField(payload, "rail");
  const status = readStringField(payload, "status");
  const state = readStringField(payload, "state");
  const credits = readNumberField(payload, "credits");

  // A missing or unrecognised state is a decode failure, never a default. A
  // wrong default here would show a payer the wrong outcome.
  if (
    !paymentIntent ||
    !rail ||
    !status ||
    !isCheckoutReturnState(state) ||
    credits === null ||
    !Number.isFinite(credits)
  ) {
    throw new Error("Failed to parse payment status response");
  }

  return {
    payment_intent_id: paymentIntent,
    rail,
    status,
    state,
    credits,
  };
}

export async function getApiKeys(): Promise<ApiKey[]> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/api-keys`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch API keys"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse API keys response");
  }

  const rawItems = readArrayField(payload, "items") ?? [];
  const keys: ApiKey[] = [];
  for (const item of rawItems) {
    const decoded = decodeApiKey(item);
    if (decoded) {
      keys.push(decoded);
    }
  }

  return keys;
}

export async function createApiKey(nickname: string, expiresAt?: string): Promise<ApiKey> {
  const { baseUrl, headers } = await getRequestContext();
  const body: { nickname: string; expires_at?: string } = { nickname };
  if (expiresAt) {
    body.expires_at = expiresAt;
  }

  const response = await fetch(`${baseUrl}/api/v1/accounts/current/api-keys`, {
    method: "POST",
    headers,
    cache: "no-store",
    body: JSON.stringify(body),
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to create API key");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse API key response");
  }

  const key = decodeApiKey(payload);
  if (!key) {
    throw new Error("Failed to parse API key response");
  }

  return key;
}

export async function revokeApiKey(keyId: string): Promise<ApiKey> {
  const { baseUrl, headers } = await getRequestContext();
  // Encoded for the same reason keyLimitsUrl encodes: a key id carrying a path
  // separator would otherwise retarget this request at a different upstream
  // path while still carrying the caller's bearer.
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/api-keys/${encodeURIComponent(keyId)}/revoke`, {
    method: "POST",
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to revoke API key");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse API key response");
  }

  const key = decodeApiKey(payload);
  if (!key) {
    throw new Error("Failed to parse API key response");
  }

  return key;
}

export async function rotateApiKey(
  keyId: string,
  nickname: string,
  expiresAt?: string
): Promise<ApiKey> {
  const { baseUrl, headers } = await getRequestContext();
  const body: { nickname: string; expires_at?: string } = { nickname };
  if (expiresAt) {
    body.expires_at = expiresAt;
  }

  const response = await fetch(`${baseUrl}/api/v1/accounts/current/api-keys/${keyId}/rotate`, {
    method: "POST",
    headers,
    cache: "no-store",
    body: JSON.stringify(body),
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to rotate API key"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse API key response");
  }

  const key = decodeApiKey(payload);
  if (!key) {
    throw new Error("Failed to parse API key response");
  }

  return key;
}

// getApiKeyLimits and updateApiKeyLimits are the only entry points for the
// per-key rate-limit surface. Issue #552: the limits read used to live in
// lib/api-keys.ts behind an injected fetch client and a bare relative path,
// which a Server Component cannot resolve (no origin, so Node's fetch rejects
// the URL) and which carried no session bearer either. Both now use the same
// absolute-URL plus session-header context as every other call in this file,
// and the page calls them directly instead of round-tripping through the
// console's own origin.
function keyLimitsUrl(baseUrl: string, keyId: string): string {
  return `${baseUrl}/api/v1/accounts/current/api-keys/${encodeURIComponent(keyId)}/limits`;
}

async function decodeKeyLimits(response: Response): Promise<KeyLimits> {
  const payload = parseJsonValue(await readResponseText(response));
  const limits = parseKeyLimits(payload);
  if (limits === null) {
    throw new Error("Failed to parse key limits response");
  }
  return limits;
}

export async function getApiKeyLimits(keyId: string): Promise<KeyLimits> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(keyLimitsUrl(baseUrl, keyId), {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to fetch key limits");
  }

  return decodeKeyLimits(response);
}

export async function updateApiKeyLimits(
  keyId: string,
  input: KeyLimitsInput,
): Promise<KeyLimits> {
  // Range validation runs before the round-trip so an obviously bad value
  // never reaches the control-plane, and it runs here rather than only in the
  // browser form so a hand-rolled action call is bounded too.
  const validationError = validateLimits(input);
  if (validationError !== null) {
    throw new Error(validationError);
  }

  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(keyLimitsUrl(baseUrl, keyId), {
    method: "PUT",
    headers,
    cache: "no-store",
    body: JSON.stringify({
      rpm: input.rpm,
      tpm: input.tpm,
      tier_overrides: input.tier_overrides,
    }),
  });

  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to update key limits");
  }

  return decodeKeyLimits(response);
}

export async function getCatalogModels(): Promise<CatalogModel[]> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/catalog/models`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch catalog models"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse catalog models response");
  }

  const rawModels = readArrayField(payload, "models") ?? [];
  const models: CatalogModel[] = [];
  for (const item of rawModels) {
    const decoded = decodeCatalogModel(item);
    if (decoded) {
      models.push(decoded);
    }
  }

  return models;
}

export interface UsageSummaryRow {
  group_key: string;
  total_input_tokens: number;
  total_output_tokens: number;
  total_credits_spent: number;
  request_count: number;
}

export interface SpendSummaryRow {
  group_key: string;
  total_credits: number;
  entry_count: number;
}

export interface ErrorSummaryRow {
  group_key: string;
  error_count: number;
  total_requests: number;
  error_rate: number;
}

export interface BudgetThreshold {
  id: string;
  threshold_credits: number;
  alert_dismissed: boolean;
  last_notified_at: string | null;
  created_at: string;
  updated_at: string;
}

function decodeUsageSummaryRow(value: JsonValue): UsageSummaryRow | null {
  if (!isJsonObject(value)) {
    return null;
  }
  const groupKey = readStringField(value, "group_key");
  if (!groupKey) {
    return null;
  }
  return {
    group_key: groupKey,
    total_input_tokens: readNumberField(value, "total_input_tokens") ?? 0,
    total_output_tokens: readNumberField(value, "total_output_tokens") ?? 0,
    total_credits_spent: readNumberField(value, "total_credits_spent") ?? 0,
    request_count: readNumberField(value, "request_count") ?? 0,
  };
}

function decodeSpendSummaryRow(value: JsonValue): SpendSummaryRow | null {
  if (!isJsonObject(value)) {
    return null;
  }
  const groupKey = readStringField(value, "group_key");
  if (!groupKey) {
    return null;
  }
  return {
    group_key: groupKey,
    total_credits: readNumberField(value, "total_credits") ?? 0,
    entry_count: readNumberField(value, "entry_count") ?? 0,
  };
}

function decodeErrorSummaryRow(value: JsonValue): ErrorSummaryRow | null {
  if (!isJsonObject(value)) {
    return null;
  }
  const groupKey = readStringField(value, "group_key");
  if (!groupKey) {
    return null;
  }
  return {
    group_key: groupKey,
    error_count: readNumberField(value, "error_count") ?? 0,
    total_requests: readNumberField(value, "total_requests") ?? 0,
    error_rate: readNumberField(value, "error_rate") ?? 0,
  };
}

function decodeBudgetThreshold(value: JsonValue): BudgetThreshold | null {
  if (!isJsonObject(value)) {
    return null;
  }
  const id = readStringField(value, "id");
  const thresholdCredits = readNumberField(value, "threshold_credits");
  const alertDismissed = readBooleanField(value, "alert_dismissed");
  const createdAt = readStringField(value, "created_at");
  const updatedAt = readStringField(value, "updated_at");

  if (!id || thresholdCredits === null || alertDismissed === null || !createdAt || !updatedAt) {
    return null;
  }

  return {
    id,
    threshold_credits: thresholdCredits,
    alert_dismissed: alertDismissed,
    last_notified_at: readStringField(value, "last_notified_at"),
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

export async function getAnalyticsUsage(params: {
  group_by: string;
  window?: string;
  from?: string;
  to?: string;
}): Promise<UsageSummaryRow[]> {
  const { baseUrl, headers } = await getRequestContext();
  const qs = new URLSearchParams({ group_by: params.group_by });
  if (params.window) qs.set("window", params.window);
  if (params.from) qs.set("from", params.from);
  if (params.to) qs.set("to", params.to);

  const response = await fetch(
    `${baseUrl}/api/v1/accounts/current/analytics/usage?${qs.toString()}`,
    { headers, cache: "no-store" }
  );

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch usage analytics"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse usage analytics response");
  }

  // handleAnalyticsUsage (apps/control-plane/internal/usage/http.go) wraps
  // its rows under "usage", never "data" (issue #856).
  const rawData = requireArrayField(payload, "usage", "Failed to parse usage analytics response");
  const rows: UsageSummaryRow[] = [];
  for (const item of rawData) {
    const decoded = decodeUsageSummaryRow(item);
    if (decoded) rows.push(decoded);
  }
  return rows;
}

export async function getAnalyticsSpend(params: {
  group_by: string;
  window?: string;
  from?: string;
  to?: string;
}): Promise<SpendSummaryRow[]> {
  const { baseUrl, headers } = await getRequestContext();
  const qs = new URLSearchParams({ group_by: params.group_by });
  if (params.window) qs.set("window", params.window);
  if (params.from) qs.set("from", params.from);
  if (params.to) qs.set("to", params.to);

  const response = await fetch(
    `${baseUrl}/api/v1/accounts/current/analytics/spend?${qs.toString()}`,
    { headers, cache: "no-store" }
  );

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch spend analytics"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse spend analytics response");
  }

  // handleAnalyticsSpend (apps/control-plane/internal/usage/http.go) wraps
  // its rows under "spend", never "data" (issue #856).
  const rawData = requireArrayField(payload, "spend", "Failed to parse spend analytics response");
  const rows: SpendSummaryRow[] = [];
  for (const item of rawData) {
    const decoded = decodeSpendSummaryRow(item);
    if (decoded) rows.push(decoded);
  }
  return rows;
}

export async function getAnalyticsErrors(params: {
  group_by: string;
  window?: string;
  from?: string;
  to?: string;
}): Promise<ErrorSummaryRow[]> {
  const { baseUrl, headers } = await getRequestContext();
  const qs = new URLSearchParams({ group_by: params.group_by });
  if (params.window) qs.set("window", params.window);
  if (params.from) qs.set("from", params.from);
  if (params.to) qs.set("to", params.to);

  const response = await fetch(
    `${baseUrl}/api/v1/accounts/current/analytics/errors?${qs.toString()}`,
    { headers, cache: "no-store" }
  );

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch error analytics"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse error analytics response");
  }

  // handleAnalyticsErrors (apps/control-plane/internal/usage/http.go) wraps
  // its rows under "errors", never "data" (issue #856).
  const rawData = requireArrayField(payload, "errors", "Failed to parse error analytics response");
  const rows: ErrorSummaryRow[] = [];
  for (const item of rawData) {
    const decoded = decodeErrorSummaryRow(item);
    if (decoded) rows.push(decoded);
  }
  return rows;
}

// =============================================================================
// Usage events (console request log browser, /console/logs)
// =============================================================================

// One usage event as the console renders it. The control-plane response
// deliberately omits provider_request_id and internal_metadata; this shape
// carries only what the table and its detail expansion show.
export interface UsageEventRow {
  id: string;
  request_id: string;
  request_attempt_id: string;
  event_type: string;
  endpoint: string;
  model_alias: string;
  status: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
  hive_credit_delta: number;
  customer_tags: Record<string, unknown>;
  error_code?: string;
  error_type?: string;
  api_key_id?: string;
  created_at: string;
}

export interface UsageEventsPage {
  events: UsageEventRow[];
  next_cursor: string | null;
}

export interface UsageLogsFilters {
  limit?: number;
  window?: string;
  modelAlias?: string;
  status?: string;
  apiKeyId?: string;
  errorsOnly?: boolean;
  cursor?: string;
}

export async function getUsageEvents(
  filters: UsageLogsFilters,
): Promise<UsageEventsPage> {
  const { baseUrl, headers } = await getRequestContext();

  const qs = new URLSearchParams();
  if (filters.limit !== undefined) {
    qs.set("limit", String(filters.limit));
  }
  if (filters.window) qs.set("window", filters.window);
  if (filters.modelAlias) qs.set("model_alias", filters.modelAlias);
  if (filters.status) qs.set("status", filters.status);
  if (filters.apiKeyId) qs.set("api_key_id", filters.apiKeyId);
  if (filters.errorsOnly) qs.set("errors", "true");
  if (filters.cursor) qs.set("cursor", filters.cursor);

  const response = await fetch(
    `${baseUrl}/api/v1/accounts/current/usage-events?${qs.toString()}`,
    { headers, cache: "no-store" }
  );

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch usage events"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse usage events response");
  }

  // handleListEvents wraps rows under "events" and exposes the keyset cursor
  // as next_cursor (empty string when there is no further page). Same
  // loud-failure contract as the analytics wrappers above (issue #856).
  const rawData = requireArrayField(payload, "events", "Failed to parse usage events response");
  const events: UsageEventRow[] = [];
  for (const item of rawData) {
    const decoded = decodeUsageEventRow(item);
    if (!decoded) {
      throw new Error("Failed to parse usage events response");
    }
    events.push(decoded);
  }

  return {
    events,
    // Upstream writes an empty string when there is no further page; normalize
    // to null so callers can test falsiness without string comparisons.
    next_cursor: readStringField(payload, "next_cursor") || null,
  };
}

function decodeUsageEventRow(value: JsonValue): UsageEventRow | null {
  if (!isJsonObject(value)) {
    return null;
  }

  const id = readStringField(value, "id");
  const requestId = readStringField(value, "request_id");
  const requestAttemptId = readStringField(value, "request_attempt_id");
  const eventType = readStringField(value, "event_type");
  const endpoint = readStringField(value, "endpoint");
  const modelAlias = readStringField(value, "model_alias");
  const status = readStringField(value, "status");
  const createdAt = readStringField(value, "created_at");

  if (!id || !requestId || !requestAttemptId || !eventType || !endpoint || !modelAlias || !status || !createdAt) {
    return null;
  }

  const rawTags = readObjectField(value, "customer_tags");
  const customerTags: Record<string, unknown> = {};
  if (rawTags) {
    for (const [k, v] of Object.entries(rawTags)) {
      customerTags[k] = v;
    }
  }

  const apiKeyId = readStringField(value, "api_key_id");

  return {
    id,
    request_id: requestId,
    request_attempt_id: requestAttemptId,
    event_type: eventType,
    endpoint,
    model_alias: modelAlias,
    status,
    input_tokens: readNumberField(value, "input_tokens") ?? 0,
    output_tokens: readNumberField(value, "output_tokens") ?? 0,
    cache_read_tokens: readNumberField(value, "cache_read_tokens") ?? undefined,
    cache_write_tokens: readNumberField(value, "cache_write_tokens") ?? undefined,
    hive_credit_delta: readNumberField(value, "hive_credit_delta") ?? 0,
    customer_tags: customerTags,
    error_code: readStringField(value, "error_code") ?? undefined,
    error_type: readStringField(value, "error_type") ?? undefined,
    api_key_id: apiKeyId ?? undefined,
    created_at: createdAt,
  }
}

export async function getBudgetThreshold(): Promise<BudgetThreshold | null> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/budget`, {
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to fetch budget threshold"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse budget threshold response");
  }

  const thresholdValue = payload["threshold"];
  if (thresholdValue === null || thresholdValue === undefined) {
    return null;
  }

  return decodeBudgetThreshold(thresholdValue);
}

export async function upsertBudgetThreshold(thresholdCredits: number): Promise<BudgetThreshold> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/budget`, {
    method: "PUT",
    headers,
    cache: "no-store",
    body: JSON.stringify({ threshold_credits: thresholdCredits }),
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to upsert budget threshold"));
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse budget threshold response");
  }

  const thresholdValue = payload["threshold"];
  const decoded = decodeBudgetThreshold(thresholdValue ?? payload);
  if (!decoded) {
    throw new Error("Failed to parse budget threshold response");
  }
  return decoded;
}

export async function dismissBudgetAlert(): Promise<void> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/budget/dismiss`, {
    method: "POST",
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to dismiss budget alert"));
  }
}

export async function getMembers(accessToken: string): Promise<AccountMember[]> {
  const baseUrl = process.env.CONTROL_PLANE_BASE_URL;
  if (!baseUrl) {
    throw new Error("CONTROL_PLANE_BASE_URL is not configured");
  }

  const response = await fetch(`${baseUrl}/api/v1/accounts/current/members`, {
    headers: {
      Authorization: `Bearer ${accessToken}`,
      "Content-Type": "application/json",
    },
    cache: "no-store",
  });

  // ControlPlaneError rather than a bare Error: the members page has to tell a
  // plain member "only owners can see the member list" apart from a real outage,
  // and it can only do that from the upstream status.
  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to fetch members");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    return [];
  }

  return decodeMembers(payload);
}

// =============================================================================
// Phase 14 — workspace budget / spend-alert / invoice surface (BDT-only).
//
// All amounts are BDT subunits (paisa). The control-plane returns `int64`
// values (Phase 14 design — see `apps/control-plane/internal/budgets/http.go`
// `budgetWireFormat`). math/big is the source-of-truth on the backend; the
// console treats subunits as fixed-precision integers via `number` with
// safe-integer guards. No USD / FX fields anywhere on this surface.
// =============================================================================

export interface BudgetSettings {
  workspace_id: string;
  period_start: string;
  soft_cap_bdt_subunits: number;
  hard_cap_bdt_subunits: number;
  currency: string;
  created_at: string;
  updated_at: string;
}

export interface SpendAlert {
  id: string;
  workspace_id: string;
  threshold_pct: number;
  email: string | null;
  webhook_url: string | null;
  last_fired_at: string | null;
  last_fired_period: string | null;
  created_at: string;
}

export interface InvoiceLineItem {
  model_id: string;
  request_count: number;
  // BDT subunit decimal string. Wire shape is JSON string (Go `,string` tag)
  // so callers parse with BigInt and never round at 2^53.
  bdt_subunits: string;
}

export interface InvoiceRecord {
  id: string;
  workspace_id: string;
  period_start: string;
  period_end: string;
  // BDT subunit decimal string — see InvoiceLineItem.bdt_subunits comment.
  total_bdt_subunits: string;
  line_items: InvoiceLineItem[];
  generated_at: string;
}

export interface UpdateBudgetInput {
  soft_cap_bdt_subunits: number;
  hard_cap_bdt_subunits: number;
  period_start?: string;
}

export interface CreateSpendAlertInput {
  threshold_pct: number;
  email?: string | null;
  webhook_url?: string | null;
  webhook_secret?: string | null;
}

export interface UpdateSpendAlertInput {
  email?: string | null;
  webhook_url?: string | null;
  webhook_secret?: string | null;
}

function decodeBudgetSettings(value: JsonValue | null): BudgetSettings | null {
  if (!isJsonObject(value)) return null;
  const workspaceId = readStringField(value, "workspace_id");
  const periodStart = readStringField(value, "period_start");
  const softCap = readNumberField(value, "soft_cap_bdt_subunits");
  const hardCap = readNumberField(value, "hard_cap_bdt_subunits");
  const currency = readStringField(value, "currency");
  const createdAt = readStringField(value, "created_at");
  const updatedAt = readStringField(value, "updated_at");
  if (
    workspaceId === null ||
    periodStart === null ||
    softCap === null ||
    hardCap === null ||
    currency === null ||
    createdAt === null ||
    updatedAt === null
  ) {
    return null;
  }
  return {
    workspace_id: workspaceId,
    period_start: periodStart,
    soft_cap_bdt_subunits: softCap,
    hard_cap_bdt_subunits: hardCap,
    currency,
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

function decodeSpendAlert(value: JsonValue): SpendAlert | null {
  if (!isJsonObject(value)) return null;
  const id = readStringField(value, "id");
  const workspaceId = readStringField(value, "workspace_id");
  const thresholdPct = readNumberField(value, "threshold_pct");
  const createdAt = readStringField(value, "created_at");
  if (
    id === null ||
    workspaceId === null ||
    thresholdPct === null ||
    createdAt === null
  ) {
    return null;
  }
  return {
    id,
    workspace_id: workspaceId,
    threshold_pct: thresholdPct,
    email: readStringField(value, "email"),
    webhook_url: readStringField(value, "webhook_url"),
    last_fired_at: readStringField(value, "last_fired_at"),
    last_fired_period: readStringField(value, "last_fired_period"),
    created_at: createdAt,
  };
}

function decodeInvoiceLineItem(value: JsonValue): InvoiceLineItem | null {
  if (!isJsonObject(value)) return null;
  const modelId = readStringField(value, "model_id");
  const requestCount = readNumberField(value, "request_count");
  // bdt_subunits arrives as a JSON string (server `,string` tag) for BigInt
  // safety. Tolerate JSON number too for forward-compat with any older test
  // fixture that still emits a number.
  const bdtSubunitsRaw = readStringField(value, "bdt_subunits");
  const bdtSubunitsNumeric = readNumberField(value, "bdt_subunits");
  const bdtSubunits =
    bdtSubunitsRaw !== null
      ? bdtSubunitsRaw
      : bdtSubunitsNumeric !== null
      ? String(bdtSubunitsNumeric)
      : null;
  if (modelId === null || requestCount === null || bdtSubunits === null) {
    return null;
  }
  return {
    model_id: modelId,
    request_count: requestCount,
    bdt_subunits: bdtSubunits,
  };
}

function decodeInvoiceRecord(value: JsonValue): InvoiceRecord | null {
  if (!isJsonObject(value)) return null;
  const id = readStringField(value, "id");
  const workspaceId = readStringField(value, "workspace_id");
  const periodStart = readStringField(value, "period_start");
  const periodEnd = readStringField(value, "period_end");
  const totalRaw = readStringField(value, "total_bdt_subunits");
  const totalNumeric = readNumberField(value, "total_bdt_subunits");
  const total =
    totalRaw !== null
      ? totalRaw
      : totalNumeric !== null
      ? String(totalNumeric)
      : null;
  const generatedAt = readStringField(value, "generated_at");
  const itemsRaw = readArrayField(value, "line_items") ?? [];
  if (
    id === null ||
    workspaceId === null ||
    periodStart === null ||
    periodEnd === null ||
    total === null ||
    generatedAt === null
  ) {
    return null;
  }
  const items: InvoiceLineItem[] = [];
  for (const it of itemsRaw) {
    const decoded = decodeInvoiceLineItem(it);
    if (decoded) items.push(decoded);
  }
  return {
    id,
    workspace_id: workspaceId,
    period_start: periodStart,
    period_end: periodEnd,
    total_bdt_subunits: total,
    line_items: items,
    generated_at: generatedAt,
  };
}

export async function getBudget(
  workspaceId: string,
): Promise<BudgetSettings | null> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/budgets/${workspaceId}`, {
    headers,
    cache: "no-store",
  });
  if (!response.ok) {
    if (response.status === 404) return null;
    await throwControlPlaneError(response, "Failed to fetch budget");
  }
  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) return null;
  const budgetField = payload["budget"];
  if (budgetField === null || budgetField === undefined) return null;
  return decodeBudgetSettings(budgetField);
}

export async function updateBudget(
  workspaceId: string,
  input: UpdateBudgetInput,
): Promise<BudgetSettings> {
  const { baseUrl, headers } = await getRequestContext();
  const body: Record<string, JsonValue> = {
    soft_cap_bdt_subunits: input.soft_cap_bdt_subunits,
    hard_cap_bdt_subunits: input.hard_cap_bdt_subunits,
  };
  if (input.period_start) {
    body.period_start = input.period_start;
  }
  const response = await fetch(`${baseUrl}/api/v1/budgets/${workspaceId}`, {
    method: "PUT",
    headers,
    body: JSON.stringify(body),
    cache: "no-store",
  });
  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to update budget");
  }
  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse budget response");
  }
  const decoded = decodeBudgetSettings(payload["budget"] ?? null);
  if (decoded === null) {
    throw new Error("Failed to parse budget response");
  }
  return decoded;
}

export async function deleteBudget(workspaceId: string): Promise<void> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/budgets/${workspaceId}`, {
    method: "DELETE",
    headers,
    cache: "no-store",
  });
  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to delete budget");
  }
}

export async function listSpendAlerts(workspaceId: string): Promise<SpendAlert[]> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/spend-alerts/${workspaceId}`, {
    headers,
    cache: "no-store",
  });
  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to fetch spend alerts");
  }
  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) return [];
  const raw = readArrayField(payload, "alerts") ?? [];
  const alerts: SpendAlert[] = [];
  for (const item of raw) {
    const decoded = decodeSpendAlert(item);
    if (decoded) alerts.push(decoded);
  }
  return alerts;
}

export async function createSpendAlert(
  workspaceId: string,
  input: CreateSpendAlertInput,
): Promise<SpendAlert> {
  const { baseUrl, headers } = await getRequestContext();
  const body: Record<string, JsonValue> = {
    threshold_pct: input.threshold_pct,
  };
  if (input.email !== undefined) body.email = input.email;
  if (input.webhook_url !== undefined) body.webhook_url = input.webhook_url;
  if (input.webhook_secret !== undefined) {
    body.webhook_secret = input.webhook_secret;
  }
  const response = await fetch(`${baseUrl}/api/v1/spend-alerts/${workspaceId}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
    cache: "no-store",
  });
  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to create spend alert");
  }
  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse spend alert response");
  }
  const alertField = payload["alert"];
  if (alertField === null || alertField === undefined) {
    throw new Error("Spend alert response missing alert field");
  }
  const decoded = decodeSpendAlert(alertField);
  if (decoded === null) {
    throw new Error("Failed to parse spend alert response");
  }
  return decoded;
}

export async function updateSpendAlert(
  workspaceId: string,
  alertId: string,
  input: UpdateSpendAlertInput,
): Promise<SpendAlert> {
  const { baseUrl, headers } = await getRequestContext();
  const body: Record<string, JsonValue> = {};
  if (input.email !== undefined) body.email = input.email;
  if (input.webhook_url !== undefined) body.webhook_url = input.webhook_url;
  if (input.webhook_secret !== undefined) {
    body.webhook_secret = input.webhook_secret;
  }
  const response = await fetch(
    `${baseUrl}/api/v1/spend-alerts/${workspaceId}/${alertId}`,
    {
      method: "PATCH",
      headers,
      body: JSON.stringify(body),
      cache: "no-store",
    },
  );
  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to update spend alert");
  }
  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse spend alert response");
  }
  const alertField = payload["alert"];
  if (alertField === null || alertField === undefined) {
    throw new Error("Spend alert response missing alert field");
  }
  const decoded = decodeSpendAlert(alertField);
  if (decoded === null) {
    throw new Error("Failed to parse spend alert response");
  }
  return decoded;
}

export async function deleteSpendAlert(
  workspaceId: string,
  alertId: string,
): Promise<void> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(
    `${baseUrl}/api/v1/spend-alerts/${workspaceId}/${alertId}`,
    { method: "DELETE", headers, cache: "no-store" },
  );
  if (!response.ok) {
    await throwControlPlaneError(response, "Failed to delete spend alert");
  }
}

export async function listWorkspaceInvoices(
  workspaceId: string,
  limit: number = 50,
): Promise<InvoiceRecord[]> {
  const { baseUrl, headers } = await getRequestContext();
  const url = new URL(`${baseUrl}/api/v1/invoices`);
  url.searchParams.set("workspace_id", workspaceId);
  url.searchParams.set("limit", String(limit));
  const response = await fetch(url.toString(), { headers, cache: "no-store" });
  if (!response.ok) {
    if (response.status === 404) return [];
    await throwControlPlaneError(response, "Failed to fetch invoices");
  }
  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) return [];
  const raw = readArrayField(payload, "items") ?? [];
  const items: InvoiceRecord[] = [];
  for (const it of raw) {
    const decoded = decodeInvoiceRecord(it);
    if (decoded) items.push(decoded);
  }
  return items;
}

export async function getWorkspaceInvoice(
  invoiceId: string,
): Promise<InvoiceRecord | null> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/invoices/${invoiceId}`, {
    headers,
    cache: "no-store",
  });
  if (!response.ok) {
    if (response.status === 404) return null;
    throw new Error(
      await readResponseError(response, "Failed to fetch invoice"),
    );
  }
  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) return null;
  const invoiceField = payload["invoice"];
  if (invoiceField === null || invoiceField === undefined) return null;
  return decodeInvoiceRecord(invoiceField);
}

// getInvoicePdfUrl performs the redirect handshake server-side and returns the
// signed Supabase Storage URL. The control-plane responds with 302 + Location;
// fetch's `redirect: "manual"` lets us read the header without auto-following.
export async function getInvoicePdfUrl(invoiceId: string): Promise<string | null> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(
    `${baseUrl}/api/v1/invoices/${invoiceId}/pdf`,
    { headers, redirect: "manual", cache: "no-store" },
  );
  if (response.status === 302 || response.status === 301) {
    const location = response.headers.get("Location");
    return location ?? null;
  }
  if (!response.ok) {
    throw new Error(
      await readResponseError(response, "Failed to resolve invoice PDF URL"),
    );
  }
  // Some edge proxies follow redirects despite `redirect: manual` — fall back
  // to body parse in that case.
  const payload = parseJsonValue(await readResponseText(response));
  if (isJsonObject(payload)) {
    return readStringField(payload, "url");
  }
  return null;
}
