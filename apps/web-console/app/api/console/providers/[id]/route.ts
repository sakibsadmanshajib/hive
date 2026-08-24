import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import {
  ControlPlaneError,
  updateProvider,
  type UpsertProviderInput,
} from "@/lib/control-plane/client";

interface RouteParams {
  params: Promise<{ id: string }>;
}

// Server-side proxy for editing one registered custom provider. The upstream
// PUT replaces the full record (no partial update), so the body carries every
// field; the control-plane is the authority on permission (platform-admin)
// and on validation.
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
  const raw = await request.json().catch((): Record<string, unknown> => ({}));
  const asString = (value: unknown): string =>
    typeof value === "string" ? value : "";
  const input: UpsertProviderInput = {
    slug: asString(raw.slug).trim(),
    display_name: asString(raw.display_name).trim(),
    base_url: asString(raw.base_url).trim(),
    api_key_env: asString(raw.api_key_env).trim(),
    litellm_prefix: asString(raw.litellm_prefix).trim(),
    enabled: raw.enabled === true,
  };

  if (input.slug === "") {
    return NextResponse.json({ error: "A slug is required." }, { status: 400 });
  }
  if (input.api_key_env === "") {
    return NextResponse.json(
      { error: "An API key environment variable name is required." },
      { status: 400 },
    );
  }

  try {
    const provider = await updateProvider(id, input);
    return NextResponse.json(provider);
  } catch (err) {
    if (err instanceof ControlPlaneError) {
      const status =
        err.status === 400 || err.status === 403 || err.status === 404 || err.status === 409
          ? err.status
          : 502;
      return NextResponse.json({ error: providerErrorMessage(err.status) }, { status });
    }
    return NextResponse.json(
      { error: "Could not update the provider. Please try again." },
      { status: 500 },
    );
  }
}

// providerErrorMessage maps an upstream status class to a generic,
// customer-safe message. It never forwards raw upstream or internal text.
function providerErrorMessage(status: number): string {
  switch (status) {
    case 400:
      return "That provider could not be validated. Check the slug, key variable name and base URL format.";
    case 403:
      return "You do not have permission to manage providers.";
    case 404:
      return "That provider could not be found.";
    case 409:
      return "A provider with that slug already exists.";
    default:
      return "Could not complete the provider request. Please try again.";
  }
}
