import { timingSafeEqual } from "node:crypto";

export function hasValidAdminToken(
  requestHeaders: Record<string, unknown> | undefined,
  expectedToken: string | undefined,
): boolean {
  if (typeof expectedToken !== "string" || expectedToken.length === 0) {
    return false;
  }

  const providedToken = requestHeaders?.["x-admin-token"];
  if (typeof providedToken !== "string") {
    return false;
  }

  const expected = Buffer.from(expectedToken, "utf8");
  const provided = Buffer.from(providedToken, "utf8");
  if (expected.length !== provided.length) {
    return false;
  }

  return timingSafeEqual(provided, expected);
}
