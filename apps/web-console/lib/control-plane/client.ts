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
