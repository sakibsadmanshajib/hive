import { NextResponse } from "next/server";
import { getServerApiBase } from "../../../../../../lib/api";
import { isSameOriginBrowserRequest } from "../../../../guest-session/request";
import { parseGuestSession, parseGuestSessionFromValue } from "../../../../guest-session/session";

async function buildProxyResponse(response: Response): Promise<NextResponse> {
  const contentType = response.headers?.get?.("content-type") ?? "";
  if (typeof response.text !== "function") {
    if (typeof response.json === "function") {
      return NextResponse.json(await response.json(), { status: response.status });
    }
    return NextResponse.json({}, { status: response.status });
  }
  const rawBody = await response.text();
  if (contentType.includes("application/json")) {
    try {
      const json = rawBody ? JSON.parse(rawBody) : {};
      return NextResponse.json(json, { status: response.status });
    } catch {
      return NextResponse.json({ error: rawBody || "invalid upstream response" }, { status: response.status });
    }
  }
  return new NextResponse(rawBody, {
    status: response.status,
    headers: contentType ? { "content-type": contentType } : undefined,
  });
}

export async function GET(
  request: Request,
  context: { params: Promise<{ sessionId: string }> },
) {
  const guestToken = process.env.WEB_INTERNAL_GUEST_TOKEN;
  if (!guestToken) {
    return NextResponse.json({ error: "guest chat unavailable" }, { status: 503 });
  }
  if (!isSameOriginBrowserRequest(request)) {
    return NextResponse.json({ error: "forbidden" }, { status: 403 });
  }
  const guestSession =
    parseGuestSession(request.headers.get("cookie"), guestToken) ??
    parseGuestSessionFromValue(request.headers.get("x-guest-session"), guestToken);
  if (!guestSession) {
    return NextResponse.json({ error: "missing guest session" }, { status: 401 });
  }

  const { sessionId } = await context.params;
  if (!sessionId?.trim()) {
    return NextResponse.json({ error: "missing session id" }, { status: 400 });
  }

  const response = await fetch(
    `${getServerApiBase()}/v1/internal/guest/chat/sessions/${encodeURIComponent(sessionId.trim())}`,
    {
      method: "GET",
      headers: {
        "x-web-guest-token": guestToken,
        "x-guest-id": guestSession.guestId,
      },
    },
  );

  return buildProxyResponse(response);
}
