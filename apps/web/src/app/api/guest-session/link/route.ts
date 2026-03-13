import { NextResponse } from "next/server";
import { getApiBase } from "../../../../lib/api";
import { isSameOriginBrowserRequest } from "../request";
import { parseGuestSession } from "../session";

export async function POST(request: Request) {
  const guestToken = process.env.WEB_INTERNAL_GUEST_TOKEN;
  if (!guestToken) {
    return NextResponse.json({ error: "guest chat unavailable" }, { status: 503 });
  }
  if (!isSameOriginBrowserRequest(request)) {
    return NextResponse.json({ error: "forbidden" }, { status: 403 });
  }

  const authorization = request.headers.get("authorization");
  if (!authorization) {
    return NextResponse.json({ error: "missing authorization" }, { status: 401 });
  }

  const guestSession = parseGuestSession(request.headers.get("cookie"), guestToken);
  if (!guestSession) {
    return NextResponse.json({ error: "missing guest session" }, { status: 401 });
  }

  const response = await fetch(`${getApiBase()}/v1/internal/guest/link`, {
    method: "POST",
    headers: {
      authorization,
      "x-web-guest-token": guestToken,
      "x-guest-id": guestSession.guestId,
    },
  });

  const body = await response.json();
  return NextResponse.json(body, { status: response.status });
}
