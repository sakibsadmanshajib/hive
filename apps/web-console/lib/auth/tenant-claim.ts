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

    // atob yields one char per byte; decode those bytes as UTF-8 so non-ASCII
    // claims elsewhere in the payload do not corrupt the parse. Avoids
    // depending on Buffer, which is not guaranteed on the Workers runtime.
    const binary = atob(payloadSegment.replace(/-/g, "+").replace(/_/g, "/"));
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
