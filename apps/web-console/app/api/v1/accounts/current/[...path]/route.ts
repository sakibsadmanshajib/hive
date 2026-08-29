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
import { NextResponse, type NextRequest } from "next/server";

import { createClient } from "@/lib/supabase/server";
import {
  ControlPlaneError,
  createApiKey,
  getCheckoutRails,
  getLedgerEntries,
  initiateCheckout,
  revokeApiKey,
  updateApiKeyBudget,
  type UpdateApiKeyBudgetInput,
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

// The four client functions this route calls raise ControlPlaneError, so an
// upstream refusal keeps its status here instead of collapsing into 502. A
// member who is not allowed to create a key sees 403, an already revoked key
// sees 409, and only a genuine transport or decode failure reads as 502.
// codeSummary states the real reason for a refusal the customer can act on,
// keyed on the upstream machine code rather than on upstream text, the same way
// app/api/console/members/role/route.ts already does. The wording lives here,
// so nothing the control-plane wrote reaches the browser.
//
// account_not_provisioned: the workspace has no tenant_billing_accounts row, so
// the key would have been refused by the API on its first request (issue
// #1330). "Conflict" is what this used to read as, which is exactly as opaque
// as the 403 the customer used to receive later.
function codeSummary(code: string | null): string | null {
  switch (code) {
    case "account_not_provisioned":
      return "This workspace is not connected to billing yet, so a key created here would be rejected by the API. If you have more than one workspace, switch to the one that carries your billing and create the key there. Otherwise reload this page to finish workspace setup, then try again.";
    default:
      return null;
  }
}

function errorResponse(err: unknown, fallback: string): Response {
  if (err instanceof ControlPlaneError) {
    const status = err.status >= 400 && err.status < 600 ? err.status : 502;
    return NextResponse.json(
      { error: codeSummary(err.code) ?? statusSummary(status) },
      { status },
    );
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

function isBudgetKind(value: unknown): value is UpdateApiKeyBudgetInput["budgetKind"] {
  return value === "none" || value === "lifetime" || value === "monthly";
}

export async function GET(request: NextRequest, { params }: Params): Promise<Response> {
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

  // GET /api/v1/accounts/current/credits/ledger?request_id=...
  // Read-only lifecycle lookup backing the request-log detail expansion. The
  // control-plane scopes every returned entry to the caller's account; the
  // proxy only narrows which request_id is fetched.
  //
  // nextUrl, not request.url: Next derives request.url from the server's own
  // bind address, and reading it is banned by
  // tools/lint-no-request-url-origin.mjs.
  if (path.length === 2 && path[0] === "credits" && path[1] === "ledger") {
    const requestId = request.nextUrl.searchParams.get("request_id");
    try {
      return NextResponse.json(
        await getLedgerEntries({
          limit: 50,
          requestId: requestId ?? undefined,
        })
      );
    } catch (err) {
      return errorResponse(err, "Failed to load ledger entries");
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

  // POST /api/v1/accounts/current/api-keys/{keyId}/policy
  //
  // Sets or clears a key's credit cap. This is the browser's only path to the
  // control-plane's real enforcement mechanism (apps/edge-api authz.CheckAccess
  // reads budget_kind/budget_limit_credits off the resolved snapshot on every
  // request) -- there is no separate "display-only" limit anywhere in this
  // surface, so this route is what makes the New Key modal's credit limit
  // field actually bind, not just render.
  if (path.length === 3 && path[0] === "api-keys" && path[2] === "policy") {
    if (!UUID_RE.test(path[1])) {
      return NextResponse.json({ error: "Not found" }, { status: 404 });
    }
    const body = await readBody(request);
    const budgetKind = body.budget_kind;
    if (!isBudgetKind(budgetKind)) {
      return NextResponse.json(
        { error: "budget_kind must be none, lifetime, or monthly" },
        { status: 400 },
      );
    }
    let budgetLimitCredits: number | null = null;
    if (budgetKind !== "none") {
      const rawLimit = body.budget_limit_credits;
      // Same float64-corruption guard as the checkout branch above: a limit
      // past Number.MAX_SAFE_INTEGER is already rounded by the time it is
      // parsed, and forwarding it would set an enforcement ceiling the
      // customer never actually chose.
      if (
        typeof rawLimit !== "number" ||
        !Number.isFinite(rawLimit) ||
        !Number.isInteger(rawLimit) ||
        rawLimit <= 0 ||
        rawLimit > Number.MAX_SAFE_INTEGER
      ) {
        return NextResponse.json(
          {
            error:
              "budget_limit_credits must be a positive integer within Number.MAX_SAFE_INTEGER when budget_kind is not none",
          },
          { status: 400 },
        );
      }
      budgetLimitCredits = rawLimit;
    }
    try {
      await updateApiKeyBudget(path[1], { budgetKind, budgetLimitCredits });
      return NextResponse.json({ ok: true });
    } catch (err) {
      return errorResponse(err, "Failed to update the key's credit limit");
    }
  }

  // POST /api/v1/accounts/current/checkout/initiate
  if (path.length === 2 && path[0] === "checkout" && path[1] === "initiate") {
    const body = await readBody(request);
    const rail = body.rail;
    const credits = body.credits;
    const idempotencyKey = body.idempotency_key;
    // Number.MAX_SAFE_INTEGER guard, the same one the sibling money proxy
    // applies in app/api/budget/[workspaceId]/route.ts. Number.isInteger is
    // true for 9007199254740993, but that value is not representable, so it
    // arrives here already rounded to 9007199254740992. Forwarding it would
    // put a silently altered purchase quantity on the wire, which is the
    // float64 corruption this project uses math/big to avoid on the Go side.
    // Reject rather than coerce: the customer asked for a quantity we cannot
    // carry, so the honest answer is a 400, not a different purchase.
    if (
      typeof rail !== "string" ||
      rail === "" ||
      typeof credits !== "number" ||
      !Number.isFinite(credits) ||
      !Number.isInteger(credits) ||
      credits <= 0 ||
      credits > Number.MAX_SAFE_INTEGER ||
      typeof idempotencyKey !== "string" ||
      idempotencyKey === ""
    ) {
      return NextResponse.json(
        {
          error:
            "rail and idempotency_key are required, and credits must be a positive integer within Number.MAX_SAFE_INTEGER",
        },
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
