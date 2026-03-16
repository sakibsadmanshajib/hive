import { NextResponse } from "next/server";
import { getServerApiBase } from "../../../lib/api";
import { isSameOriginBrowserRequest, readClientIp } from "./request";
import { buildGuestSessionCookie, createGuestSession } from "./session";

const INTERNAL_REQUEST_TIMEOUT_MS = 5_000;

export async function POST(request: Request) {
  const secret = process.env.WEB_INTERNAL_GUEST_TOKEN;
  if (!secret) {
    return NextResponse.json({ error: "guest chat unavailable" }, { status: 503 });
  }
  if (!isSameOriginBrowserRequest(request)) {
    return NextResponse.json({ error: "forbidden" }, { status: 403 });
  }

  const { cookieValue, session } = createGuestSession(secret);
  const clientIp = readClientIp(request);
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), INTERNAL_REQUEST_TIMEOUT_MS);
  let persisted: Response;
  try {
    persisted = await fetch(`${getServerApiBase()}/v1/internal/guest/session`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-web-guest-token": secret,
      },
      body: JSON.stringify({
        guestId: session.guestId,
        expiresAt: session.expiresAt,
        ...(clientIp ? { lastSeenIp: clientIp } : {}),
      }),
      signal: controller.signal,
    });
  } catch (error) {
    console.error("guest session bootstrap failed", error);
    return NextResponse.json({ error: "guest chat unavailable" }, { status: 502 });
  } finally {
    clearTimeout(timeout);
  }
  if (!persisted.ok) {
    return NextResponse.json({ error: "guest chat unavailable" }, { status: 502 });
  }

  const response = NextResponse.json(
    { ...session, cookieValue },
    { status: 200 },
  );
  response.headers.set("set-cookie", buildGuestSessionCookie(cookieValue, session.expiresAt));
  return response;
}
