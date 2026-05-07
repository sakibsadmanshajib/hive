// Phase 14 FIX-14-26 — workspace spend-alerts proxy route.
//
// Proxies POST /api/spend-alerts/{workspaceId} → control-plane
// /api/v1/spend-alerts/{workspaceId}. Owner-only on the backend.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createClient } from "@/lib/supabase/server";
import {
  createSpendAlert,
  type CreateSpendAlertInput,
} from "@/lib/control-plane/client";

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

function parseCreateInput(body: unknown): CreateSpendAlertInput | null {
  if (body === null || typeof body !== "object") return null;
  const record: Record<string, unknown> = body as Record<string, unknown>;
  const threshold = record.threshold_pct;
  if (
    typeof threshold !== "number" ||
    !Number.isFinite(threshold) ||
    !(threshold === 50 || threshold === 80 || threshold === 100)
  ) {
    return null;
  }
  const out: CreateSpendAlertInput = { threshold_pct: threshold };
  const email = record.email;
  if (email === null || typeof email === "string") out.email = email;
  const webhook = record.webhook_url;
  if (webhook === null || typeof webhook === "string") out.webhook_url = webhook;
  const secret = record.webhook_secret;
  if (secret === null || typeof secret === "string") out.webhook_secret = secret;
  return out;
}

export async function POST(
  request: Request,
  { params }: { params: Promise<{ workspaceId: string }> },
): Promise<Response> {
  const unauth = await requireUser();
  if (unauth) return unauth;
  const { workspaceId } = await params;
  try {
    const body: unknown = await request.json();
    const input = parseCreateInput(body);
    if (input === null) {
      return NextResponse.json(
        { error: "threshold_pct must be one of 50, 80, 100" },
        { status: 400 },
      );
    }
    const alert = await createSpendAlert(workspaceId, input);
    return NextResponse.json({ alert });
  } catch (err) {
    const message =
      err instanceof Error ? err.message : "Failed to create alert";
    return NextResponse.json({ error: message }, { status: 500 });
  }
}
