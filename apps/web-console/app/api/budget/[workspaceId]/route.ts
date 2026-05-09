// Phase 14 FIX-14-25 — workspace budget proxy route.
//
// Proxies PUT/DELETE /api/budget/{workspaceId} → control-plane
// /api/v1/budgets/{workspaceId}. Owner-only on the backend. Forwards
// upstream HTTP status verbatim instead of collapsing to 500. Never
// surfaces raw upstream messages — that would leak internal details.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createClient } from "@/lib/supabase/server";
import {
  ControlPlaneError,
  deleteBudget,
  updateBudget,
  type UpdateBudgetInput,
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

function parseUpdateInput(body: unknown): UpdateBudgetInput | null {
  if (body === null || typeof body !== "object") return null;
  const record: Record<string, unknown> = body as Record<string, unknown>;
  const soft = record.soft_cap_bdt_subunits;
  const hard = record.hard_cap_bdt_subunits;
  // Number.MAX_SAFE_INTEGER guard: PostgreSQL BIGINT column can hold values
  // beyond 2^53, but JavaScript Number loses precision past that boundary.
  // Reject any input above MAX_SAFE_INTEGER so the wire-shape never carries
  // a silently-rounded BIGINT for currency subunits.
  if (
    typeof soft !== "number" ||
    typeof hard !== "number" ||
    !Number.isFinite(soft) ||
    !Number.isFinite(hard) ||
    !Number.isInteger(soft) ||
    !Number.isInteger(hard) ||
    soft < 0 ||
    hard < 0 ||
    soft > Number.MAX_SAFE_INTEGER ||
    hard > Number.MAX_SAFE_INTEGER
  ) {
    return null;
  }
  const out: UpdateBudgetInput = {
    soft_cap_bdt_subunits: soft,
    hard_cap_bdt_subunits: hard,
  };
  if (typeof record.period_start === "string") {
    out.period_start = record.period_start;
  }
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

export async function PUT(
  request: Request,
  { params }: { params: Promise<{ workspaceId: string }> },
): Promise<Response> {
  const unauth = await requireUser();
  if (unauth) return unauth;
  const { workspaceId } = await params;
  try {
    const body: unknown = await request.json();
    const input = parseUpdateInput(body);
    if (input === null) {
      return NextResponse.json(
        {
          error:
            "soft_cap_bdt_subunits and hard_cap_bdt_subunits must be non-negative integers within Number.MAX_SAFE_INTEGER",
        },
        { status: 400 },
      );
    }
    const budget = await updateBudget(workspaceId, input);
    return NextResponse.json({ budget });
  } catch (err) {
    return errorResponse(err, "Failed to update budget");
  }
}

export async function DELETE(
  _request: Request,
  { params }: { params: Promise<{ workspaceId: string }> },
): Promise<Response> {
  const unauth = await requireUser();
  if (unauth) return unauth;
  const { workspaceId } = await params;
  try {
    await deleteBudget(workspaceId);
    return NextResponse.json({ ok: true });
  } catch (err) {
    return errorResponse(err, "Failed to delete budget");
  }
}
