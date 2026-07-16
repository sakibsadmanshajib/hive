import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import { ControlPlaneError, setMarketplaceEntryEnabled } from "@/lib/control-plane/client";

interface PutBody {
  enabled?: boolean;
}

interface RouteParams {
  params: Promise<{ id: string }>;
}

// Server-side proxy for enabling/disabling one marketplace catalog entry for
// the caller's tenant (issue #309). Keeps the internal CONTROL_PLANE_BASE_URL
// server-only and attaches the caller's session bearer; the control-plane is
// the authority on permission (platform-admin) and on entry existence.
export async function PUT(request: Request, { params }: RouteParams): Promise<Response> {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const {
    data: { user },
    error,
  } = await supabase.auth.getUser();
  if (error || !user) {
    return NextResponse.json({ error: "Unauthorized" }, { status: 401 });
  }

  const { id } = await params;
  const body: PutBody = await request.json().catch((): PutBody => ({}));
  if (typeof body.enabled !== "boolean") {
    return NextResponse.json({ error: "enabled must be a boolean" }, { status: 400 });
  }

  try {
    const result = await setMarketplaceEntryEnabled(id, body.enabled);
    return NextResponse.json(result);
  } catch (err) {
    if (err instanceof ControlPlaneError) {
      const status = err.status === 403 || err.status === 404 ? err.status : 502;
      return NextResponse.json({ error: marketplaceErrorMessage(err.status) }, { status });
    }
    return NextResponse.json(
      { error: "Could not update the marketplace entry. Please try again." },
      { status: 500 },
    );
  }
}

// marketplaceErrorMessage maps an upstream status class to a generic,
// customer-safe message. It never forwards raw upstream or internal text.
function marketplaceErrorMessage(status: number): string {
  switch (status) {
    case 403:
      return "You do not have permission to manage the marketplace.";
    case 404:
      return "That marketplace entry could not be found.";
    default:
      return "Could not complete the marketplace request. Please try again.";
  }
}
