import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";

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

export interface ViewerGates {
  can_invite_members: boolean;
  can_manage_api_keys: boolean;
}

export interface Viewer {
  user: ViewerUser;
  current_account: ViewerAccount;
  memberships: ViewerMembership[];
  gates: ViewerGates;
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
  gates: ViewerGates;
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

async function getRequestContext(): Promise<RequestContext> {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);

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
}

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

function decodeViewerResponse(payload: JsonObject): ViewerResponse | null {
  const user = readObjectField(payload, "user");
  const currentAccount = readObjectField(payload, "current_account");
  const gates = readObjectField(payload, "gates");
  const membershipsValue = readArrayField(payload, "memberships");

  if (!user || !currentAccount || !gates || !membershipsValue) {
    return null;
  }

  const userId = readStringField(user, "id");
  const userEmail = readStringField(user, "email");
  const userEmailVerified = readBooleanField(user, "email_verified");
  const currentAccountId = readStringField(currentAccount, "id");
  const currentAccountDisplayName = readStringField(currentAccount, "display_name");
  const currentAccountType = readStringField(currentAccount, "account_type");
  const currentAccountRole = readStringField(currentAccount, "role");
  const canInviteMembers = readBooleanField(gates, "can_invite_members");
  const canManageApiKeys = readBooleanField(gates, "can_manage_api_keys");

  if (
    !userId ||
    !userEmail ||
    userEmailVerified === null ||
    !currentAccountId ||
    !currentAccountDisplayName ||
    !currentAccountType ||
    !currentAccountRole ||
    canInviteMembers === null ||
    canManageApiKeys === null
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
    gates: {
      can_invite_members: canInviteMembers,
      can_manage_api_keys: canManageApiKeys,
    },
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
    gates: rawViewer.gates,
  };
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
    return {
      owner_name: "",
      login_email: "",
      display_name: "",
      account_type: "",
      country_code: "",
      state_region: "",
      profile_setup_complete: false,
    };
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

export async function getBillingProfile(): Promise<BillingProfile> {
  const { baseUrl, headers } = await getRequestContext();
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/billing-profile`, {
    headers,
    cache: "no-store",
  });

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
  amount_usd: number;
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
  price_per_credit_usd: number;
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
    input_price_credits: number;
    output_price_credits: number;
    cache_read_price_credits: number | null;
    cache_write_price_credits: number | null;
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
  const amountUsd = readNumberField(value, "amount_usd") ?? 0;
  const amountLocal = readNumberField(value, "amount_local") ?? 0;
  const localCurrency = readStringField(value, "local_currency") ?? "USD";
  const taxTreatment = readStringField(value, "tax_treatment") ?? "";
  const rail = readStringField(value, "rail") ?? "";
  const createdAt = readStringField(value, "created_at");

  if (!id || !createdAt) {
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
    amount_usd: amountUsd,
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
  const inputPrice = rawPricing ? readNumberField(rawPricing, "input_price_credits") ?? 0 : 0;
  const outputPrice = rawPricing ? readNumberField(rawPricing, "output_price_credits") ?? 0 : 0;
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
    throw new Error(await readResponseError(response, "Failed to fetch checkout rails"));
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

  const creditIncrement = readNumberField(payload, "credit_increment") ?? 1000;
  const minCredits = readNumberField(payload, "min_credits") ?? 1000;
  const maxCredits = readNumberField(payload, "max_credits") ?? 100000;
  const pricePerCreditUsd = readNumberField(payload, "price_per_credit_usd") ?? 0.01;

  return {
    rails,
    credit_increment: creditIncrement,
    min_credits: minCredits,
    max_credits: maxCredits,
    price_per_credit_usd: pricePerCreditUsd,
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
    throw new Error(await readResponseError(response, "Failed to initiate checkout"));
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
  const localCurrency = readStringField(payload, "local_currency") ?? "USD";

  return {
    payment_intent_id: paymentIntentId,
    redirect_url: redirectUrl,
    rail: responsRail,
    credits: responseCredits,
    amount_local: amountLocal,
    local_currency: localCurrency,
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
    throw new Error(await readResponseError(response, "Failed to create API key"));
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
  const response = await fetch(`${baseUrl}/api/v1/accounts/current/api-keys/${keyId}/revoke`, {
    method: "POST",
    headers,
    cache: "no-store",
  });

  if (!response.ok) {
    throw new Error(await readResponseError(response, "Failed to revoke API key"));
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

  const rawData = readArrayField(payload, "data") ?? [];
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

  const rawData = readArrayField(payload, "data") ?? [];
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

  const rawData = readArrayField(payload, "data") ?? [];
  const rows: ErrorSummaryRow[] = [];
  for (const item of rawData) {
    const decoded = decodeErrorSummaryRow(item);
    if (decoded) rows.push(decoded);
  }
  return rows;
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

  if (!response.ok) {
    throw new Error(`Failed to fetch members: ${response.status}`);
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    return [];
  }

  return decodeMembers(payload);
}
