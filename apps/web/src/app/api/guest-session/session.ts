import { createHmac, randomUUID, timingSafeEqual } from "node:crypto";

export type IssuedGuestSession = {
  guestId: string;
  issuedAt: string;
  expiresAt: string;
};

const COOKIE_NAME = "hive_guest_session";
const DEFAULT_TTL_MS = 1000 * 60 * 60 * 24 * 7;

function signPayload(payload: string, secret: string): string {
  return createHmac("sha256", secret).update(payload).digest("base64url");
}

export function createGuestSession(secret: string, now = new Date()): { cookieValue: string; session: IssuedGuestSession } {
  const issuedAt = now.toISOString();
  const expiresAt = new Date(now.getTime() + DEFAULT_TTL_MS).toISOString();
  const session: IssuedGuestSession = {
    guestId: `guest_${randomUUID().replace(/-/g, "").slice(0, 20)}`,
    issuedAt,
    expiresAt,
  };
  const payload = Buffer.from(JSON.stringify(session)).toString("base64url");
  const signature = signPayload(payload, secret);

  return {
    cookieValue: `${payload}.${signature}`,
    session,
  };
}

function parseSignedGuestValue(rawValue: string, secret: string): IssuedGuestSession | null {
  const [payload, signature] = rawValue.split(".");
  if (!payload || !signature) {
    return null;
  }
  const expectedSignature = signPayload(payload, secret);
  const received = Buffer.from(signature);
  const expected = Buffer.from(expectedSignature);
  if (received.length !== expected.length || !timingSafeEqual(received, expected)) {
    return null;
  }
  try {
    const parsed = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as IssuedGuestSession;
    if (!parsed.guestId || !parsed.issuedAt || !parsed.expiresAt) {
      return null;
    }
    if (new Date(parsed.expiresAt).getTime() <= Date.now()) {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}

export function parseGuestSession(cookieHeader: string | null, secret: string): IssuedGuestSession | null {
  if (!cookieHeader) {
    return null;
  }
  const cookiePart = cookieHeader
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${COOKIE_NAME}=`));
  if (!cookiePart) {
    return null;
  }
  const rawValue = cookiePart.slice(`${COOKIE_NAME}=`.length);
  return parseSignedGuestValue(rawValue, secret);
}

/** Parse guest session from raw cookie value (e.g. from x-guest-session header). Same verification as cookie. */
export function parseGuestSessionFromValue(value: string | null, secret: string): IssuedGuestSession | null {
  if (!value?.trim()) {
    return null;
  }
  return parseSignedGuestValue(value.trim(), secret);
}

export function buildGuestSessionCookie(cookieValue: string, expiresAt: string): string {
  const secure = process.env.NODE_ENV === "production" ? " Secure;" : "";
  return `${COOKIE_NAME}=${cookieValue}; Path=/; HttpOnly;${secure} SameSite=Lax; Expires=${new Date(expiresAt).toUTCString()}`;
}
