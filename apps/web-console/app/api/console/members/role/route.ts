import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import { ControlPlaneError, updateMemberRole } from "@/lib/control-plane/client";
import { parseMemberRole } from "@/lib/members/roles";
import { resolveCanonicalOrigin } from "@/lib/http/origin";
import { refuseCrossOrigin } from "@/lib/http/same-origin";

// Server-side proxy for changing a member's workspace role (issue #536).
//
// The members table posts a plain HTML <form method="POST"> (no client JS), so
// the outcome travels back as a redirect: success sets role_updated=1, failure
// sets a generic, customer-safe `error` message that the members page renders.
//
// Authorization is decided entirely by the control-plane, which owns the
// permission policy plus the two invariants that protect a workspace (nobody
// changes their own role, and the last owner cannot be demoted). This handler
// only proves the caller has a session, validates the shape of the request, and
// translates the upstream refusal into copy a customer can act on.
export async function POST(request: Request): Promise<Response> {
  // Cross-origin refusal, before the session lookup (issue #1457).
  const refusal = refuseCrossOrigin(request);
  if (refusal) return refusal;

  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const {
    data: { user },
    error,
  } = await supabase.auth.getUser();
  if (error || !user) {
    return NextResponse.json({ error: "Unauthorized" }, { status: 401 });
  }

  const fields = await readFields(request);

  const userId = typeof fields.userId === "string" ? fields.userId.trim() : "";
  if (userId === "") {
    return NextResponse.json({ error: "A member is required" }, { status: 400 });
  }

  const role = parseMemberRole(fields.role);
  if (role === null) {
    return NextResponse.json({ error: "A valid role is required" }, { status: 400 });
  }

  try {
    await updateMemberRole(userId, role);
  } catch (err) {
    return redirectToMembers(request, { error: roleErrorMessage(err) });
  }

  return redirectToMembers(request, { role_updated: "1" });
}

async function readFields(
  request: Request,
): Promise<{ userId?: unknown; role?: unknown }> {
  const contentType = request.headers.get("content-type") ?? "";
  if (contentType.includes("application/json")) {
    const body: unknown = await request.json().catch(() => null);
    if (body === null || typeof body !== "object") return {};
    const record = body as Record<string, unknown>;
    return { userId: record.user_id, role: record.role };
  }
  const form = await request.formData().catch(() => null);
  if (!form) return {};
  return { userId: form.get("user_id"), role: form.get("role") };
}

// roleErrorMessage states the real reason a role change was refused, keyed on the
// upstream machine code (status alone is ambiguous: two different refusals share
// 403). Raw upstream text never reaches the browser-visible redirect URL, history
// or logs.
function roleErrorMessage(err: unknown): string {
  const generic = "Could not update the member role. Please try again.";
  if (!(err instanceof ControlPlaneError)) {
    return generic;
  }
  switch (err.code) {
    case "last_owner_required":
      return "The workspace must keep at least one owner, so that role change was not applied.";
    case "self_role_change_forbidden":
      return "You cannot change your own role. Ask another owner to do it.";
    case "permission_denied":
      return "You do not have permission to change member roles.";
    case "member_not_found":
      return "That member is no longer part of this workspace.";
    case "invalid_role":
      return "Pick either Owner or Member.";
    default:
      break;
  }
  switch (err.status) {
    case 400:
      return "Pick either Owner or Member.";
    case 403:
      return "You do not have permission to change member roles.";
    case 404:
      return "That member is no longer part of this workspace.";
    case 409:
      return "The workspace must keep at least one owner, so that role change was not applied.";
    default:
      return generic;
  }
}

function redirectToMembers(
  request: Request,
  params: Record<string, string>,
): Response {
  // Resolve against the canonical app origin (not the spoofable request host)
  // so this 303 cannot be steered to another origin via X-Forwarded-Host.
  const url = new URL("/console/members", resolveCanonicalOrigin(request));
  for (const [key, value] of Object.entries(params)) {
    url.searchParams.set(key, value);
  }
  return NextResponse.redirect(url, 303);
}
