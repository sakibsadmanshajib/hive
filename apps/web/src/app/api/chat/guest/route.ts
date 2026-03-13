import { NextResponse } from "next/server";
import { getApiBase } from "../../../../lib/api";
import { isSameOriginBrowserRequest, readClientIp } from "../../guest-session/request";
import { parseGuestSession } from "../../guest-session/session";

export async function POST(request: Request) {
  const guestToken = process.env.WEB_INTERNAL_GUEST_TOKEN;
  if (!guestToken) {
    return NextResponse.json({ error: "guest chat unavailable" }, { status: 503 });
  }
  if (!isSameOriginBrowserRequest(request)) {
    return NextResponse.json({ error: "forbidden" }, { status: 403 });
  }
  const guestSession = parseGuestSession(request.headers.get("cookie"), guestToken);
  if (!guestSession) {
    return NextResponse.json({ error: "missing guest session" }, { status: 401 });
  }

  const payload = await request.json();
  const clientIp = readClientIp(request);
  const response = await fetch(`${getApiBase()}/v1/internal/chat/guest`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-web-guest-token": guestToken,
      "x-guest-id": guestSession.guestId,
      ...(clientIp ? { "x-guest-client-ip": clientIp } : {}),
    },
    body: JSON.stringify(payload),
  });

  const body = await response.json();
  return NextResponse.json(body, { status: response.status });
}
