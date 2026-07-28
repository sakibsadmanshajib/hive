import { NextResponse } from "next/server";

import { resolveCanonicalOrigin } from "@/lib/http/origin";
import {
  BILLING_PATH,
  CHECKOUT_RETURN_PATH,
  parseIntentId,
  parseReturnHint,
} from "@/lib/payments/checkout-return";

// SSLCommerz returns the paying customer's browser with a cross-site form POST,
// which an App Router page cannot serve. This handler exists only to absorb that
// POST and redirect the browser onward to the return page (issue #538).
//
// It is deliberately inert:
//   - It settles nothing. Settlement is driven by the SSLCommerz IPN, which is a
//     server-to-server POST to the control-plane and is hash verified plus
//     re-validated against SSLCommerz before any credit is granted.
//   - It reads no state out of the POST body beyond the transaction id, and it
//     forwards no caller-supplied outcome. The return page resolves the
//     authoritative state from the payment intent record.
//   - It requires no session. The cross-site POST would not carry SameSite=Lax
//     cookies anyway; the redirect that follows is a top-level navigation, so the
//     return page itself is authenticated normally.

async function intentIdFromRequest(request: Request): Promise<string | null> {
  const url = new URL(request.url);
  const fromQuery = parseIntentId(url.searchParams.get("intent"));
  if (fromQuery) return fromQuery;

  if (request.method !== "POST") return null;

  // SSLCommerz echoes our payment intent id back as tran_id.
  try {
    const body = await request.text();
    return parseIntentId(new URLSearchParams(body).get("tran_id"));
  } catch {
    return null;
  }
}

function redirectTo(request: Request, path: string, params: URLSearchParams): Response {
  const target = new URL(path, resolveCanonicalOrigin(request));
  target.search = params.toString();
  // 303 so the browser follows with a GET regardless of how it arrived here.
  return NextResponse.redirect(target, 303);
}

async function handle(request: Request): Promise<Response> {
  const intentId = await intentIdFromRequest(request);

  if (!intentId) {
    // Nothing to look up. Send the customer somewhere useful rather than to an
    // error page: the ledger and balance on the billing page are authoritative.
    return redirectTo(request, BILLING_PATH, new URLSearchParams({ checkout: "unknown" }));
  }

  const params = new URLSearchParams({ rail: "sslcommerz", intent: intentId });
  const hint = parseReturnHint(new URL(request.url).searchParams.get("hint"));
  if (hint) {
    params.set("hint", hint);
  }

  return redirectTo(request, CHECKOUT_RETURN_PATH, params);
}

export async function POST(request: Request): Promise<Response> {
  return handle(request);
}

export async function GET(request: Request): Promise<Response> {
  return handle(request);
}
