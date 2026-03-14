import { NextResponse } from "next/server";
import { getServerApiBase } from "../../../lib/api";
import { isSameOriginBrowserRequest, readClientIp } from "./request";
import { buildGuestSessionCookie, createGuestSession } from "./session";

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
  const persisted = await fetch(`${getServerApiBase()}/v1/internal/guest/session`, {
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
  });
  if (!persisted.ok) {
    return NextResponse.json({ error: "guest chat unavailable" }, { status: 502 });
  }

  const response = NextResponse.json(session, { status: 200 });
  response.headers.set("set-cookie", buildGuestSessionCookie(cookieValue, session.expiresAt));
  return response;
}
