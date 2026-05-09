// Phase 14 FIX-14-26 — single spend-alert proxy route (PATCH/DELETE).
//
// Forwards upstream HTTP status (4xx/5xx) verbatim instead of collapsing
// every failure to 500. Never surfaces raw upstream messages — that would
// leak internal details onto a customer surface.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createClient } from "@/lib/supabase/server";
import {
  ControlPlaneError,
  deleteSpendAlert,
  updateSpendAlert,
  type UpdateSpendAlertInput,
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

function parseUpdateInput(body: unknown): UpdateSpendAlertInput {
  const out: UpdateSpendAlertInput = {};
  if (body === null || typeof body !== "object") return out;
  const record: Record<string, unknown> = body as Record<string, unknown>;
  const email = record.email;
  if (email === null || typeof email === "string") out.email = email;
  const webhook = record.webhook_url;
  if (webhook === null || typeof webhook === "string") out.webhook_url = webhook;
  const secret = record.webhook_secret;
  if (secret === null || typeof secret === "string") out.webhook_secret = secret;
  return out;
}

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

export async function PATCH(
  request: Request,
  { params }: { params: Promise<{ workspaceId: string; alertId: string }> },
): Promise<Response> {
  const unauth = await requireUser();
  if (unauth) return unauth;
  const { workspaceId, alertId } = await params;
  try {
    const body: unknown = await request.json().catch(() => ({}));
    const input = parseUpdateInput(body);
    const alert = await updateSpendAlert(workspaceId, alertId, input);
    return NextResponse.json({ alert });
  } catch (err) {
    return errorResponse(err, "Failed to update alert");
  }
}

export async function DELETE(
  _request: Request,
  { params }: { params: Promise<{ workspaceId: string; alertId: string }> },
): Promise<Response> {
  const unauth = await requireUser();
  if (unauth) return unauth;
  const { workspaceId, alertId } = await params;
  try {
    await deleteSpendAlert(workspaceId, alertId);
    return NextResponse.json({ ok: true });
  } catch (err) {
    return errorResponse(err, "Failed to delete alert");
  }
}
