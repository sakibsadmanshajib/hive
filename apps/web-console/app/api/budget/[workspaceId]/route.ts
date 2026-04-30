// Phase 14 FIX-14-25 — workspace budget proxy route.
//
// Proxies PUT/DELETE /api/budget/{workspaceId} → control-plane
// /api/v1/budgets/{workspaceId}. Owner-only on the backend.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createClient } from "@/lib/supabase/server";
import {
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
  if (
    typeof soft !== "number" ||
    typeof hard !== "number" ||
    !Number.isFinite(soft) ||
    !Number.isFinite(hard) ||
    soft < 0 ||
    hard < 0
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
            "soft_cap_bdt_subunits and hard_cap_bdt_subunits must be non-negative numbers",
        },
        { status: 400 },
      );
    }
    const budget = await updateBudget(workspaceId, input);
    return NextResponse.json({ budget });
  } catch (err) {
    const message =
      err instanceof Error ? err.message : "Failed to update budget";
    return NextResponse.json({ error: message }, { status: 500 });
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
    const message =
      err instanceof Error ? err.message : "Failed to delete budget";
    return NextResponse.json({ error: message }, { status: 500 });
  }
}
