import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import {
  ControlPlaneError,
  createInvitation,
  type InvitationCreated,
} from "@/lib/control-plane/client";
import { parseMemberRole } from "@/lib/members/roles";
import { resolveCanonicalOrigin } from "@/lib/http/origin";
import { refuseCrossOrigin } from "@/lib/http/same-origin";

// Server-side proxy for issuing a workspace invitation (issues #111, #1440).
//
// The invite form used to POST cross-origin straight at the control-plane with
// `action={process.env.CONTROL_PLANE_BASE_URL}/...`, which (a) leaked the
// internal control-plane URL into the rendered HTML and (b) sent the request
// from the browser without the user's session bearer. This handler keeps the
// control-plane address server-only and attaches auth via the client helper.
//
// Two response shapes, chosen by the Accept header.
//
// A plain HTML <form method="POST"> still works with no client JavaScript at
// all: it gets a 303 back to the members page carrying the delivery outcome as
// a flag. The invite panel asks for JSON instead, and gets the outcome plus the
// invitation link, which it renders in place.
//
// The link is the reason the JSON shape exists. The acceptance token is
// bearer-equivalent and must never travel in a redirect URL, so it cannot ride
// the 303, and the database stores only its hash so it cannot be recovered
// afterwards. Before this change the route simply dropped it, which destroyed
// the only copy while the interface reported "Invitation sent" (issue #1440).
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
  const email = readEmail(fields.email);
  if (!email) {
    return NextResponse.json({ error: "A valid email is required" }, { status: 400 });
  }

  // The invite form carries a role selector (issue #536). An unsupported value
  // is rejected here rather than forwarded; an absent value keeps the
  // least-privileged default.
  const role = fields.role === undefined ? "member" : parseMemberRole(fields.role);
  if (role === null) {
    return NextResponse.json({ error: "A valid role is required" }, { status: 400 });
  }

  const wantsJson = (request.headers.get("accept") ?? "").includes("application/json");

  let created: InvitationCreated;
  try {
    created = await createInvitation(email, role);
  } catch (err) {
    const message = inviteErrorMessage(err);
    if (wantsJson) {
      return NextResponse.json({ error: message }, { status: 400 });
    }
    return redirectToMembers(request, { error: message });
  }

  if (wantsJson) {
    // The link is built against the canonical app origin rather than the
    // request host, so a spoofed X-Forwarded-Host cannot produce an invitation
    // link pointing somewhere else.
    const link =
      created.token === null
        ? null
        : `${resolveCanonicalOrigin(request)}/invitations/accept?token=${encodeURIComponent(created.token)}`;
    return NextResponse.json(
      {
        email,
        role,
        delivered: created.delivered,
        delivery: created.delivery,
        link,
      },
      {
        status: 201,
        // Belt and braces around a body carrying an acceptance token: no shared
        // cache, no proxy copy, no back-button replay out of the disk cache.
        headers: { "Cache-Control": "no-store, private" },
      },
    );
  }

  // No-JS path. The token cannot ride a redirect, so this shape reports the
  // delivery outcome only, and the members page tells the user plainly when
  // nothing was mailed rather than claiming a send.
  return redirectToMembers(request, { invited: created.delivery });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// readFields accepts either a plain HTML form post (the console) or JSON.
//
// Narrowed at runtime rather than asserted. This is a trust boundary, so a cast
// would be asserting a shape about a body the caller chose.
async function readFields(
  request: Request,
): Promise<{ email?: unknown; role?: unknown }> {
  const contentType = request.headers.get("content-type") ?? "";
  if (contentType.includes("application/json")) {
    const body: unknown = await request.json().catch(() => null);
    if (!isRecord(body)) return {};
    return {
      email: body.email,
      role: "role" in body ? body.role : undefined,
    };
  }
  const form = await request.formData().catch(() => null);
  if (!form) return {};
  return {
    email: form.get("email"),
    role: form.has("role") ? form.get("role") : undefined,
  };
}

function readEmail(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  const email = raw.trim();
  // Minimal shape check; the control-plane is the authority on validity.
  if (email.length === 0 || !email.includes("@")) return null;
  return email;
}

// inviteErrorMessage maps a failure to a generic, status-class message. It never
// forwards raw upstream/internal text (provider names, DB errors,
// "CONTROL_PLANE_BASE_URL is not configured") into the browser-visible redirect
// URL, history, or logs. Only the HTTP status of a ControlPlaneError informs the
// wording; every other error collapses to the generic fallback.
function inviteErrorMessage(err: unknown): string {
  const generic = "Could not create the invitation. Please try again.";
  if (!(err instanceof ControlPlaneError)) {
    return generic;
  }
  switch (err.status) {
    case 400:
      return "Please check the email address and try again.";
    case 403:
      return "You do not have permission to invite members.";
    case 404:
      return "Workspace not found.";
    case 409:
      return "That person is already a member or has a pending invite.";
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
