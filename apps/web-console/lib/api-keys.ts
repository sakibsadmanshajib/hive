// Phase 12 — KEY-05 helpers for the per-key + tier-override limits CRUD.
// Strict types: no `as`, no `any`, no `unknown` casts. Validators upgrade
// untyped JSON into strongly typed objects via narrow predicates.

export type TierName = "guest" | "unverified" | "verified" | "credited";

export const TIER_NAMES: readonly TierName[] = [
  "guest",
  "unverified",
  "verified",
  "credited",
];

export interface TierLimit {
  rpm: number;
  tpm: number;
}

export type TierOverrides = Partial<Record<TierName, TierLimit>>;

export interface KeyLimits {
  api_key_id: string;
  rpm: number;
  tpm: number;
  tier_overrides: TierOverrides;
}

export interface KeyLimitsInput {
  rpm: number;
  tpm: number;
  tier_overrides: TierOverrides;
}

export const RATE_LIMIT_RPM_MAX = 100000;
export const RATE_LIMIT_TPM_MAX = 10000000;

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isTierName(value: string): value is TierName {
  return TIER_NAMES.includes(value as TierName);
}

function parseTierLimit(input: unknown): TierLimit | null {
  if (!isRecord(input)) return null;
  const rpm = input.rpm;
  const tpm = input.tpm;
  if (typeof rpm !== "number" || typeof tpm !== "number") return null;
  return { rpm, tpm };
}

function parseTierOverrides(input: unknown): TierOverrides {
  if (!isRecord(input)) return {};
  const out: TierOverrides = {};
  for (const [key, value] of Object.entries(input)) {
    if (!isTierName(key)) continue;
    const parsed = parseTierLimit(value);
    if (parsed === null) continue;
    out[key] = parsed;
  }
  return out;
}

export function parseKeyLimits(payload: unknown): KeyLimits | null {
  if (!isRecord(payload)) return null;
  const apiKeyID = payload.api_key_id;
  const rpm = payload.rpm;
  const tpm = payload.tpm;
  if (typeof apiKeyID !== "string") return null;
  if (typeof rpm !== "number" || typeof tpm !== "number") return null;
  return {
    api_key_id: apiKeyID,
    rpm,
    tpm,
    tier_overrides: parseTierOverrides(payload.tier_overrides),
  };
}

export function validateLimits(input: KeyLimitsInput): string | null {
  if (!Number.isFinite(input.rpm) || input.rpm < 0 || input.rpm > RATE_LIMIT_RPM_MAX) {
    return `RPM must be between 0 and ${RATE_LIMIT_RPM_MAX}`;
  }
  if (!Number.isFinite(input.tpm) || input.tpm < 0 || input.tpm > RATE_LIMIT_TPM_MAX) {
    return `TPM must be between 0 and ${RATE_LIMIT_TPM_MAX}`;
  }
  for (const [tier, limit] of Object.entries(input.tier_overrides)) {
    if (!isTierName(tier)) return `Unknown tier name: ${tier}`;
    if (limit.rpm < 0 || limit.rpm > RATE_LIMIT_RPM_MAX) {
      return `Tier ${tier} RPM out of range`;
    }
    if (limit.tpm < 0 || limit.tpm > RATE_LIMIT_TPM_MAX) {
      return `Tier ${tier} TPM out of range`;
    }
  }
  return null;
}

export interface ApiKeysClient {
  fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response>;
}

export async function getKeyLimits(
  client: ApiKeysClient,
  keyID: string,
): Promise<KeyLimits> {
  const resp = await client.fetch(
    `/api/v1/accounts/current/api-keys/${encodeURIComponent(keyID)}/limits`,
    { method: "GET", headers: { Accept: "application/json" } },
  );
  if (!resp.ok) {
    throw new Error(`getKeyLimits: HTTP ${resp.status}`);
  }
  const body: unknown = await resp.json();
  const parsed = parseKeyLimits(body);
  if (parsed === null) throw new Error("getKeyLimits: invalid response shape");
  return parsed;
}

export async function updateKeyLimits(
  client: ApiKeysClient,
  keyID: string,
  input: KeyLimitsInput,
): Promise<KeyLimits> {
  const validationErr = validateLimits(input);
  if (validationErr !== null) throw new Error(validationErr);

  const resp = await client.fetch(
    `/api/v1/accounts/current/api-keys/${encodeURIComponent(keyID)}/limits`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(input),
    },
  );
  if (!resp.ok) {
    throw new Error(`updateKeyLimits: HTTP ${resp.status}`);
  }
  const body: unknown = await resp.json();
  const parsed = parseKeyLimits(body);
  if (parsed === null) throw new Error("updateKeyLimits: invalid response shape");
  return parsed;
}
