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

/**
 * Longest API key nickname the control plane will store, in characters.
 * Mirrors `MaxNicknameLen` in `apps/control-plane/internal/apikeys/types.go`,
 * which is the enforcement point; this copy exists so the form can refuse a
 * value before spending a round trip on it (issue #1400).
 */
export const MAX_KEY_NICKNAME_LEN = 100;

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

// parseKeyLimitsInput narrows a payload that arrived over the wire (a server
// action argument is attacker-controlled exactly like a request body) into the
// input shape. Range checking stays in validateLimits.
export function parseKeyLimitsInput(payload: unknown): KeyLimitsInput | null {
  if (!isRecord(payload)) return null;
  const rpm = payload.rpm;
  const tpm = payload.tpm;
  if (typeof rpm !== "number" || typeof tpm !== "number") return null;
  return {
    rpm,
    tpm,
    tier_overrides: parseTierOverrides(payload.tier_overrides),
  };
}

// SaveLimitsResult is the serialisable outcome a server action hands back to
// the browser form. Errors travel as data rather than as a thrown error, since
// Next redacts thrown server-action messages in production builds.
export type SaveLimitsResult =
  | { ok: true }
  | { ok: false; error: string };

export function validateLimits(input: KeyLimitsInput): string | null {
  if (!Number.isFinite(input.rpm) || input.rpm < 0 || input.rpm > RATE_LIMIT_RPM_MAX) {
    return `RPM must be between 0 and ${RATE_LIMIT_RPM_MAX}`;
  }
  if (!Number.isFinite(input.tpm) || input.tpm < 0 || input.tpm > RATE_LIMIT_TPM_MAX) {
    return `TPM must be between 0 and ${RATE_LIMIT_TPM_MAX}`;
  }
  for (const [tier, limit] of Object.entries(input.tier_overrides)) {
    if (!isTierName(tier)) return `Unknown tier name: ${tier}`;
    // Partial<Record<TierName, TierLimit>> permits undefined slots; an
    // explicit guard keeps strict-mode narrowing happy and treats a
    // present-but-undefined entry as "no override" rather than throwing.
    if (limit === undefined) continue;
    if (limit.rpm < 0 || limit.rpm > RATE_LIMIT_RPM_MAX) {
      return `Tier ${tier} RPM out of range`;
    }
    if (limit.tpm < 0 || limit.tpm > RATE_LIMIT_TPM_MAX) {
      return `Tier ${tier} TPM out of range`;
    }
  }
  return null;
}

// Transport lives in lib/control-plane/client.ts (getApiKeyLimits and
// updateApiKeyLimits). This module stays pure parsing and validation so the
// browser form and the server can share it without either one carrying a
// fetch client. Issue #552: the helpers that used to sit here fetched a bare
// relative path, which is unresolvable from a Server Component.

const USD_INPUT_RE = /^\d+(\.\d{1,9})?$/;

/**
 * Convert a customer-typed dollar string (the New Key modal's "Credit limit"
 * field) into an integer credit count. One US dollar is 1,000,000,000
 * credits (`.wolf/decisions.md` D-046), which is exact in IEEE 754, but many
 * decimal literals a customer types ("0.1", "12.34") are not, so this shifts
 * the decimal point on the STRING rather than multiplying floats. A limit
 * that is off by even one credit is a wrong enforcement ceiling, not a
 * cosmetic rounding difference.
 *
 * Returns null for blank input (the field is optional; blank means no cap),
 * for anything that is not a plain non-negative decimal with at most 9
 * fractional digits (1 credit's own precision), for zero (a $0 cap would
 * block every request, which is not what "$0" typed into this field means to
 * a customer -- "0 (unlimited)" is Groq/OpenRouter/upstream terminology,
 * never a real cap here), and for anything that would not survive the round
 * trip as a JS-safe integer.
 */
export function usdToCreditsInput(raw: string): number | null {
  const trimmed = raw.trim();
  if (trimmed === "") return null;
  if (!USD_INPUT_RE.test(trimmed)) return null;
  const [whole, frac = ""] = trimmed.split(".");
  const digits = `${whole}${frac.padEnd(9, "0")}`.replace(/^0+(?=\d)/, "");
  const credits = Number(digits);
  if (!Number.isSafeInteger(credits) || credits <= 0) return null;
  return credits;
}
