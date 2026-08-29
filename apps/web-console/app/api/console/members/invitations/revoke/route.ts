import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import { ControlPlaneError, revokeInvitation } from "@/lib/control-plane/client";
import { resolveCanonicalOrigin } from "@/lib/http/origin";

// Server-side proxy for withdrawing an outstanding workspace invitation
// (issue #1440).
//
// Same shape as the invite proxy: the control-plane address stays server-only
// and the user's session bearer is attached by the client helper. A plain HTML
// form target, so it takes a POST rather than a DELETE, and reports its outcome
// by redirecting back to the members page.
//
// Authorization is the control-plane's decision, not this route's. It scopes the
// delete by account in the statement itself, so an id belonging to another
// workspace comes back as a 404 rather than a cross-workspace revoke.
export async function POST(request: Request): Promise<Response> {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const {
    data: { user },
    error,
  } = await supabase.auth.getUser();
  if (error || !user) {
    return NextResponse.json({ error: "Unauthorized" }, { status: 401 });
  }

  const id = await readInvitationId(request);
  if (id === null) {
    return redirectToMembers(request, {
      error: "That invitation could not be identified.",
    });
  }

  try {
    await revokeInvitation(id);
  } catch (err) {
    return redirectToMembers(request, { error: revokeErrorMessage(err) });
  }

  return redirectToMembers(request, { revoked: "1" });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// readInvitationId accepts a plain HTML form post (the console) or JSON.
//
// Narrowed at runtime rather than asserted. A route handler is a trust
// boundary, so a cast here would be asserting a shape about a body an
// unauthenticated caller chose.
async function readInvitationId(request: Request): Promise<string | null> {
  const contentType = request.headers.get("content-type") ?? "";
  let raw: unknown;
  if (contentType.includes("application/json")) {
    const body: unknown = await request.json().catch((): unknown => null);
    if (!isRecord(body)) return null;
    raw = body.id;
  } else {
    const form = await request.formData().catch((): FormData | null => null);
    if (!form) return null;
    raw = form.get("id");
  }
  if (typeof raw !== "string") return null;
  const id = raw.trim();
  // Shape check only. The control-plane is the authority on whether the
  // invitation exists and whether this workspace may touch it.
  return id === "" ? null : id;
}

// revokeErrorMessage maps a failure to a generic, status-class message. It never
// forwards raw upstream text into a browser-visible redirect URL, history, or
// log.
function revokeErrorMessage(err: unknown): string {
  const generic = "Could not withdraw the invitation. Please try again.";
  if (!(err instanceof ControlPlaneError)) {
    return generic;
  }
  switch (err.status) {
    case 400:
      return "That invitation could not be identified.";
    case 403:
      return "You do not have permission to manage invitations.";
    case 404:
      return "That invitation no longer exists.";
    default:
      return generic;
  }
}

function redirectToMembers(
  request: Request,
  params: Record<string, string>,
): Response {
  // Resolved against the canonical app origin rather than the spoofable request
  // host, so this 303 cannot be steered elsewhere via X-Forwarded-Host.
  const url = new URL("/console/members", resolveCanonicalOrigin(request));
  for (const [key, value] of Object.entries(params)) {
    url.searchParams.set(key, value);
  }
  return NextResponse.redirect(url, 303);
}
