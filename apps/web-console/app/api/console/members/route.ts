import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import { ControlPlaneError, createInvitation } from "@/lib/control-plane/client";

// Server-side proxy for sending a workspace invite (issue #111).
//
// The invite form used to POST cross-origin straight at the control-plane with
// `action={process.env.CONTROL_PLANE_BASE_URL}/...`, which (a) leaked the
// internal control-plane URL into the rendered HTML and (b) sent the request
// from the browser without the user's session bearer. This handler keeps the
// control-plane address server-only and attaches auth via the client helper.
//
// The form is a plain HTML <form method="POST"> (no client JS), so on success
// we redirect (303) back to the members page; failures redirect back with an
// `error` query param. Auth/validation failures short-circuit before any
// upstream call.
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

  const email = await readEmail(request);
  if (!email) {
    return NextResponse.json({ error: "A valid email is required" }, { status: 400 });
  }

  try {
    await createInvitation(email);
  } catch (err) {
    const message =
      err instanceof ControlPlaneError || err instanceof Error
        ? err.message
        : "Failed to send invitation";
    return redirectToMembers(request, { error: message });
  }

  return redirectToMembers(request, { invited: "1" });
}

async function readEmail(request: Request): Promise<string | null> {
  const contentType = request.headers.get("content-type") ?? "";
  let raw: unknown;
  if (contentType.includes("application/json")) {
    const body: unknown = await request.json().catch(() => null);
    raw = body && typeof body === "object" ? (body as Record<string, unknown>).email : null;
  } else {
    const form = await request.formData().catch(() => null);
    raw = form?.get("email");
  }
  if (typeof raw !== "string") return null;
  const email = raw.trim();
  // Minimal shape check; the control-plane is the authority on validity.
  if (email.length === 0 || !email.includes("@")) return null;
  return email;
}

function redirectToMembers(
  request: Request,
  params: Record<string, string>,
): Response {
  const url = new URL("/console/members", request.url);
  for (const [key, value] of Object.entries(params)) {
    url.searchParams.set(key, value);
  }
  return NextResponse.redirect(url, 303);
}
