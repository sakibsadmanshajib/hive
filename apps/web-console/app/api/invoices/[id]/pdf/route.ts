// Phase 14 FIX-14-27 — invoice PDF download proxy.
//
// Resolves the signed Supabase Storage URL via the control-plane and
// redirects the browser there. Auth is the user's Supabase session; the
// control-plane verifies workspace membership and signs a short-lived URL.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createClient } from "@/lib/supabase/server";
import { getInvoicePdfUrl } from "@/lib/control-plane/client";

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
    const message =
      err instanceof Error ? err.message : "Failed to resolve invoice PDF";
    return NextResponse.json({ error: message }, { status: 500 });
  }
}
