// Browser-facing proxy for the control-plane account surface.
//
// Several client components fetch `/api/v1/accounts/current/...` as a relative
// path. That path only ever existed on the control-plane, never on this app, so
// Next answered every one of them with its own 404 HTML page: API keys could
// not be created or revoked, and the checkout modal could neither list payment
// rails nor start a checkout. Issue #552 fixed the same mistake on the per-key
// limits page by moving that call server-side; these callers were missed.
//
// The control-plane address and the caller's session bearer must stay
// server-only, so the browser cannot call the control-plane itself. This
// handler is the seam: it authenticates the session, then delegates to the same
// typed client every Server Component already uses.
//
// The dispatch table is an explicit allowlist, not a blanket pass-through. A
// catch-all that forwarded any path would expose every present and future
// account route to the browser, including ones meant for server use only.
// Adding a browser-reachable operation is one entry here.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import {
  ControlPlaneError,
  createApiKey,
  getCheckoutRails,
  initiateCheckout,
  revokeApiKey,
} from "@/lib/control-plane/client";

type Params = { params: Promise<{ path: string[] }> };

async function requireSession(): Promise<Response | null> {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const {
    data: { user },
    error,
  } = await supabase.auth.getUser();
  if (error || !user) {
    return NextResponse.json({ error: "Unauthorized" }, { status: 401 });
  }
  return null;
}

// statusSummary keeps upstream text out of the browser. Only the status class
// informs the wording, matching the other proxy routes in this app.
function statusSummary(status: number): string {
  if (status >= 500) return "Upstream service error";
  if (status === 401) return "Unauthorized";
  if (status === 403) return "Forbidden";
  if (status === 404) return "Not found";
  if (status === 409) return "Conflict";
  return "Request rejected";
}

function errorResponse(err: unknown, fallback: string): Response {
  if (err instanceof ControlPlaneError) {
    const status = err.status >= 400 && err.status < 600 ? err.status : 502;
    return NextResponse.json({ error: statusSummary(status) }, { status });
  }
  return NextResponse.json({ error: fallback }, { status: 502 });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

// The key id arrives from the browser and is interpolated into an upstream URL
// by revokeApiKey without encoding, so a value containing path separators would
// steer the request at a different control-plane path carrying the caller's own
// bearer. Every key id this surface issues is a UUID, so require that shape here
// rather than trusting the segment.
const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

async function readBody(request: Request): Promise<Record<string, unknown>> {
  const body: unknown = await request.json().catch(() => null);
  return isRecord(body) ? body : {};
}

export async function GET(_request: Request, { params }: Params): Promise<Response> {
  const unauth = await requireSession();
  if (unauth) return unauth;

  const { path } = await params;

  if (path.length === 2 && path[0] === "checkout" && path[1] === "rails") {
    try {
      return NextResponse.json(await getCheckoutRails());
    } catch (err) {
      return errorResponse(err, "Failed to load checkout rails");
    }
  }

  return NextResponse.json({ error: "Not found" }, { status: 404 });
}

export async function POST(request: Request, { params }: Params): Promise<Response> {
  const unauth = await requireSession();
  if (unauth) return unauth;

  const { path } = await params;

  // POST /api/v1/accounts/current/api-keys
  if (path.length === 1 && path[0] === "api-keys") {
    const body = await readBody(request);
    const nickname = body.nickname;
    if (typeof nickname !== "string" || nickname.trim() === "") {
      return NextResponse.json({ error: "nickname is required" }, { status: 400 });
    }
    const expiresAt = typeof body.expires_at === "string" ? body.expires_at : undefined;
    try {
      return NextResponse.json(await createApiKey(nickname.trim(), expiresAt));
    } catch (err) {
      return errorResponse(err, "Failed to create API key");
    }
  }

  // POST /api/v1/accounts/current/api-keys/{keyId}/revoke
  if (path.length === 3 && path[0] === "api-keys" && path[2] === "revoke") {
    if (!UUID_RE.test(path[1])) {
      return NextResponse.json({ error: "Not found" }, { status: 404 });
    }
    try {
      return NextResponse.json(await revokeApiKey(path[1]));
    } catch (err) {
      return errorResponse(err, "Failed to revoke API key");
    }
  }

  // POST /api/v1/accounts/current/checkout/initiate
  if (path.length === 2 && path[0] === "checkout" && path[1] === "initiate") {
    const body = await readBody(request);
    const rail = body.rail;
    const credits = body.credits;
    const idempotencyKey = body.idempotency_key;
    if (
      typeof rail !== "string" ||
      rail === "" ||
      typeof credits !== "number" ||
      !Number.isFinite(credits) ||
      !Number.isInteger(credits) ||
      credits <= 0 ||
      typeof idempotencyKey !== "string" ||
      idempotencyKey === ""
    ) {
      return NextResponse.json(
        { error: "rail, credits and idempotency_key are required" },
        { status: 400 },
      );
    }
    try {
      return NextResponse.json(await initiateCheckout(rail, credits, idempotencyKey));
    } catch (err) {
      return errorResponse(err, "Failed to initiate checkout");
    }
  }

  return NextResponse.json({ error: "Not found" }, { status: 404 });
}
