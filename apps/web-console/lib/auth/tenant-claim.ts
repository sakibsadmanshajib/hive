/**
 * Reads the `tenant_id` claim out of a Supabase access token.
 *
 * The signature is deliberately NOT verified here. This read only decides which
 * UI to render: the normal console, or the "no workspace yet" state. Every
 * actual authorization decision is enforced server side by control-plane,
 * edge-api, and Postgres RLS, so an unverified claim read cannot grant access
 * to anything.
 *
 * The token arrives from the Supabase SSR session cookie, which middleware has
 * already validated via getUser() (a real round-trip to Supabase that rejects
 * revoked tokens) before any console page renders.
 *
 * A membership-less user now receives a valid token with no `tenant_id` claim
 * at all, so a missing claim is an expected state, not an error.
 */
export function readTenantIdClaim(
  accessToken: string | null | undefined,
): string | null {
  if (!accessToken) {
    return null;
  }

  try {
    const payloadSegment = accessToken.split(".")[1];
    if (!payloadSegment) {
      return null;
    }

    // JWT segments are base64url and unpadded. Restore the standard alphabet
    // and the "=" padding before decoding: atob is lenient about missing
    // padding in some runtimes and strict in others, and a throw here would
    // fail safe to "no claim", which for a user who genuinely has one means
    // being sent round the provisioning redirect on every request. Pad
    // explicitly rather than relying on the runtime.
    const base64 = payloadSegment.replace(/-/g, "+").replace(/_/g, "/");
    const padded = base64.padEnd(
      base64.length + ((4 - (base64.length % 4)) % 4),
      "=",
    );
    // atob yields one char per byte; decode those bytes as UTF-8 so non-ASCII
    // claims elsewhere in the payload do not corrupt the parse. Avoids
    // depending on Buffer, which is not guaranteed on the Workers runtime.
    const binary = atob(padded);
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
    const parsed: unknown = JSON.parse(new TextDecoder().decode(bytes));

    if (
      typeof parsed !== "object" ||
      parsed === null ||
      !("tenant_id" in parsed)
    ) {
      return null;
    }

    const tenantId = parsed.tenant_id;
    return typeof tenantId === "string" && tenantId.length > 0
      ? tenantId
      : null;
  } catch {
    return null;
  }
}
