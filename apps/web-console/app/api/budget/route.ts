import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createClient } from "@/lib/supabase/server";
import { upsertBudgetThreshold, dismissBudgetAlert } from "@/lib/control-plane/client";

export const runtime = "nodejs";

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

function parseThresholdCredits(body: unknown): number | null {
  if (body === null || typeof body !== "object") return null;
  const record: Record<string, unknown> = body as Record<string, unknown>;
  const value = record.threshold_credits;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return null;
  return value;
}

export async function PUT(request: Request): Promise<Response> {
  const unauth = await requireUser();
  if (unauth) return unauth;

  try {
    const body: unknown = await request.json();
    const thresholdCredits = parseThresholdCredits(body);
    if (thresholdCredits === null) {
      return NextResponse.json(
        { error: "threshold_credits must be a non-negative number" },
        { status: 400 },
      );
    }
    const threshold = await upsertBudgetThreshold(thresholdCredits);
    return NextResponse.json({ threshold });
  } catch (err) {
    const message = err instanceof Error ? err.message : "Failed to update budget threshold";
    return NextResponse.json({ error: message }, { status: 500 });
  }
}

export async function DELETE(): Promise<Response> {
  const unauth = await requireUser();
  if (unauth) return unauth;

  try {
    await dismissBudgetAlert();
    return NextResponse.json({ ok: true });
  } catch (err) {
    const message = err instanceof Error ? err.message : "Failed to dismiss budget alert";
    return NextResponse.json({ error: message }, { status: 500 });
  }
}
