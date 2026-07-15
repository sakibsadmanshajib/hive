import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import { ControlPlaneError, setFeatureGate } from "@/lib/control-plane/client";

interface PutBody {
  key?: string;
  enabled?: boolean;
}

// Server-side proxy for toggling a feature gate (issue #292). Keeps the
// internal CONTROL_PLANE_BASE_URL server-only and attaches the caller's session
// bearer. The control-plane is the authority on permission (platform-admin) and
// on which keys exist; this handler only shape-checks and maps the upstream
// status class to a generic, customer-safe message.
export async function PUT(request: Request): Promise<Response> {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const {
    data: { user },
    error,
  } = await supabase.auth.getUser();
  if (error || !user) {
    return NextResponse.json({ error: "Unauthorized" }, { status: 401 });
  }

  const body: PutBody = await request.json().catch((): PutBody => ({}));
  if (typeof body.key !== "string" || body.key.length === 0) {
    return NextResponse.json({ error: "A gate key is required" }, { status: 400 });
  }
  if (typeof body.enabled !== "boolean") {
    return NextResponse.json({ error: "enabled must be a boolean" }, { status: 400 });
  }

  try {
    const result = await setFeatureGate(body.key, body.enabled);
    return NextResponse.json(result);
  } catch (err) {
    if (err instanceof ControlPlaneError) {
      const status =
        err.status === 400 || err.status === 403 || err.status === 404
          ? err.status
          : 502;
      return NextResponse.json({ error: gateErrorMessage(err.status) }, { status });
    }
    return NextResponse.json(
      { error: "Could not update the feature gate. Please try again." },
      { status: 500 },
    );
  }
}

// gateErrorMessage maps an upstream status class to a generic, customer-safe
// message. It never forwards raw upstream or internal text.
function gateErrorMessage(status: number): string {
  switch (status) {
    case 400:
      return "That feature gate is not recognized.";
    case 403:
      return "You do not have permission to change feature gates.";
    case 404:
      return "That feature gate could not be found.";
    default:
      return "Could not update the feature gate. Please try again.";
  }
}
