import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import { ControlPlaneError, getCheckoutIntent } from "@/lib/control-plane/client";
import { parseIntentId } from "@/lib/payments/checkout-return";

// Read-only proxy behind the return page's pending poller (issue #538).
//
// The proxy exists so the browser never holds a control-plane token. It is
// GET-only and forwards a read; it can neither settle a payment nor grant a
// credit. Ownership is enforced upstream: the control-plane scopes the lookup to
// the authenticated viewer's account and answers 404 for anything else.
export async function GET(request: Request): Promise<Response> {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const {
    data: { user },
    error,
  } = await supabase.auth.getUser();
  if (error || !user) {
    return NextResponse.json({ error: "Unauthorized" }, { status: 401 });
  }

  const intentId = parseIntentId(new URL(request.url).searchParams.get("payment_intent_id"));
  if (!intentId) {
    return NextResponse.json(
      { error: "payment_intent_id must be a UUID" },
      { status: 400 },
    );
  }

  try {
    const intent = await getCheckoutIntent(intentId);
    return NextResponse.json(intent, {
      status: 200,
      headers: { "Cache-Control": "no-store" },
    });
  } catch (err) {
    if (err instanceof ControlPlaneError) {
      return NextResponse.json({ error: err.message }, { status: err.status });
    }
    return NextResponse.json({ error: "Failed to fetch payment status" }, { status: 500 });
  }
}
