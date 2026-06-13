// Proxy route for the signup abuse-prevention precheck (issue #116).
//
// The control-plane precheck endpoint (POST /api/v1/auth/sign-up/precheck)
// requires a server-side call because CONTROL_PLANE_BASE_URL is never exposed
// to the browser. This Route Handler receives { email, captcha_token } from
// the signup page, forwards the request to the control-plane, and returns the
// upstream status and body verbatim. It never proxies upstream error detail
// that could leak internal information; only the generic message from the
// control-plane travels to the client.

import { NextResponse } from "next/server";

interface PrecheckBody {
  email: string;
  captcha_token: string;
}

function isPrecheckBody(value: unknown): value is PrecheckBody {
  if (value === null || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return typeof record.email === "string" && typeof record.captcha_token === "string";
}

export async function POST(request: Request): Promise<Response> {
  const baseUrl = process.env.CONTROL_PLANE_BASE_URL;
  if (!baseUrl) {
    return NextResponse.json(
      { error: "Service temporarily unavailable. Please try again." },
      { status: 503 }
    );
  }

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "Invalid request body." }, { status: 400 });
  }

  if (!isPrecheckBody(body)) {
    return NextResponse.json({ error: "Invalid request body." }, { status: 400 });
  }

  const upstreamUrl = `${baseUrl}/api/v1/auth/sign-up/precheck`;

  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), 5000);

  let upstream: Response;
  try {
    upstream = await fetch(upstreamUrl, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: body.email, captcha_token: body.captcha_token }),
      signal: controller.signal,
    });
  } catch {
    // Network-level failure or timeout: do not expose internal URL or error detail.
    return NextResponse.json(
      { error: "Service temporarily unavailable. Please try again." },
      { status: 503 }
    );
  } finally {
    clearTimeout(timeoutId);
  }

  let upstreamBody: Record<string, unknown>;
  try {
    upstreamBody = (await upstream.json()) as Record<string, unknown>;
  } catch {
    upstreamBody = {};
  }

  return NextResponse.json(upstreamBody, { status: upstream.status });
}
