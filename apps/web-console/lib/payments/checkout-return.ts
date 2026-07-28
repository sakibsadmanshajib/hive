// Shared vocabulary for the payment browser-return flow (issue #538).
//
// A payment rail talks to Hive over two channels. The server-to-server webhook
// on the control-plane is the only thing allowed to settle a payment. The
// browser return lands here, on the console, and only ever reports what
// settlement already decided.

/** The single console page every rail's browser return resolves to. */
export const CHECKOUT_RETURN_PATH = "/console/billing/checkout/return";

/** Where the console sends a customer whose return carried no usable intent id. */
export const BILLING_PATH = "/console/billing";

/**
 * Copy hint carried by a provider's cancel or back URL.
 *
 * TRUST BOUNDARY: a hint is a wording selector, never a state. It travels in a
 * query string the customer can edit, so it is only allowed to soften copy while
 * the authoritative intent state is still pending. It can never produce a
 * success, a failure, or a credit.
 */
export const RETURN_HINT_CANCELLED = "cancelled";

export type CheckoutReturnState = "success" | "pending" | "failed" | "cancelled";

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * parseIntentId accepts only a syntactically valid UUID. Ownership is enforced
 * server-side by the control-plane, which reports another account's intent as
 * not found, so this is input hygiene rather than an access check.
 */
export function parseIntentId(value: string | null | undefined): string | null {
  if (!value) return null;
  const trimmed = value.trim();
  return UUID_PATTERN.test(trimmed) ? trimmed.toLowerCase() : null;
}

/** Normalises the only hint the return surface honours. */
export function parseReturnHint(value: string | null | undefined): string | null {
  return value?.trim().toLowerCase() === RETURN_HINT_CANCELLED
    ? RETURN_HINT_CANCELLED
    : null;
}

export function isCheckoutReturnState(value: unknown): value is CheckoutReturnState {
  return (
    value === "success" ||
    value === "pending" ||
    value === "failed" ||
    value === "cancelled"
  );
}
