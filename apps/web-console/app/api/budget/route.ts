import { NextResponse } from "next/server";
import { upsertBudgetThreshold, dismissBudgetAlert } from "@/lib/control-plane/client";

export async function PUT(request: Request): Promise<Response> {
  try {
    const body: unknown = await request.json();
    if (
      body === null ||
      typeof body !== "object" ||
      !("threshold_credits" in body) ||
      typeof (body as { threshold_credits: unknown }).threshold_credits !== "number"
    ) {
      return NextResponse.json({ error: "threshold_credits must be a number" }, { status: 400 });
    }

    const thresholdCredits = (body as { threshold_credits: number }).threshold_credits;
    const threshold = await upsertBudgetThreshold(thresholdCredits);
    return NextResponse.json({ threshold });
  } catch (err) {
    const message = err instanceof Error ? err.message : "Failed to update budget threshold";
    return NextResponse.json({ error: message }, { status: 500 });
  }
}

export async function DELETE(): Promise<Response> {
  try {
    await dismissBudgetAlert();
    return NextResponse.json({ ok: true });
  } catch (err) {
    const message = err instanceof Error ? err.message : "Failed to dismiss budget alert";
    return NextResponse.json({ error: message }, { status: 500 });
  }
}
