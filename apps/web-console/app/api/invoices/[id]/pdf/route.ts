// Phase 14 FIX-14-27 — invoice PDF download proxy.
//
// Resolves the signed Supabase Storage URL via the control-plane and
// redirects the browser there. Auth is the user's Supabase session; the
// control-plane verifies workspace membership and signs a short-lived URL.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createClient } from "@/lib/supabase/server";
import { ControlPlaneError, getInvoicePdfUrl } from "@/lib/control-plane/client";

async function requireUser(): Promise<Response | null> {
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

const GENERIC_FAILURE = "Could not resolve the invoice PDF. Please try again.";

// Customer-facing text, chosen by status class alone. The upstream message is
// never forwarded: it is written by the control plane for operators and can
// name internal detail, which the provider-blind rule keeps off a customer
// response (CLAUDE.md, Conventions).
function pdfErrorMessage(status: number): string {
  if (status === 404) return "Invoice not found";
  if (status === 403) return "You do not have access to this invoice.";
  if (status === 400) return "That invoice reference is not valid.";
  return GENERIC_FAILURE;
}

export async function GET(
  _request: Request,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const unauth = await requireUser();
  if (unauth) return unauth;
  const { id } = await params;
  try {
    const url = await getInvoicePdfUrl(id);
    if (!url) {
      return NextResponse.json({ error: "Invoice not found" }, { status: 404 });
    }
    return NextResponse.redirect(url, 302);
  } catch (err) {
    // The catch used to answer 500 for every failure, including the 404 the
    // control plane correctly returns for an unknown invoice id, and it put
    // the upstream error text in the body (issue #1649). Status class
    // forwarded, upstream text logged and dropped.
    console.error("invoice pdf proxy: could not resolve the signed URL", err);
    if (err instanceof ControlPlaneError) {
      const status =
        err.status === 400 || err.status === 403 || err.status === 404
          ? err.status
          : 502;
      return NextResponse.json(
        { error: pdfErrorMessage(err.status) },
        { status },
      );
    }
    return NextResponse.json(
      { error: GENERIC_FAILURE },
      { status: 500 },
    );
  }
}
