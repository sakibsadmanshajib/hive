import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import {
  ControlPlaneError,
  deleteMarketplaceEntry,
  updateMarketplaceEntry,
  type MarketplaceEntryConfig,
} from "@/lib/control-plane/client";

interface PutBody {
  name?: string;
  description?: string;
  config?: MarketplaceEntryConfig;
}

interface RouteParams {
  params: Promise<{ id: string }>;
}

// Server-side proxy for editing or removing one marketplace catalog entry
// (issue #309). Keeps the internal CONTROL_PLANE_BASE_URL server-only and
// attaches the caller's session bearer; the control-plane is the authority
// on permission (platform-admin) and on existence.
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
  if (typeof body.name !== "string" || body.name.trim().length === 0) {
    return NextResponse.json({ error: "A name is required" }, { status: 400 });
  }
  if (body.config !== undefined && typeof body.config !== "object") {
    return NextResponse.json({ error: "config must be a JSON object" }, { status: 400 });
  }

  try {
    const entry = await updateMarketplaceEntry(id, {
      name: body.name,
      description: typeof body.description === "string" ? body.description : "",
      config: body.config ?? {},
    });
    return NextResponse.json(entry);
  } catch (err) {
    if (err instanceof ControlPlaneError) {
      const status =
        err.status === 400 || err.status === 403 || err.status === 404 || err.status === 409
          ? err.status
          : 502;
      return NextResponse.json({ error: marketplaceErrorMessage(err.status) }, { status });
    }
    return NextResponse.json(
      { error: "Could not update the marketplace entry. Please try again." },
      { status: 500 },
    );
  }
}

export async function DELETE(_request: Request, { params }: RouteParams): Promise<Response> {
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
  try {
    await deleteMarketplaceEntry(id);
    return NextResponse.json({ status: "deleted" });
  } catch (err) {
    if (err instanceof ControlPlaneError) {
      const status = err.status === 403 || err.status === 404 ? err.status : 502;
      return NextResponse.json({ error: marketplaceErrorMessage(err.status) }, { status });
    }
    return NextResponse.json(
      { error: "Could not delete the marketplace entry. Please try again." },
      { status: 500 },
    );
  }
}

// marketplaceErrorMessage maps an upstream status class to a generic,
// customer-safe message. It never forwards raw upstream or internal text.
function marketplaceErrorMessage(status: number): string {
  switch (status) {
    case 400:
      return "That marketplace entry could not be validated.";
    case 403:
      return "You do not have permission to manage the marketplace.";
    case 404:
      return "That marketplace entry could not be found.";
    case 409:
      return "An entry with that kind and name already exists.";
    default:
      return "Could not complete the marketplace request. Please try again.";
  }
}
