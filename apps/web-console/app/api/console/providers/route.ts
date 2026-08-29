import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { createClient } from "@/lib/supabase/server";
import {
  ControlPlaneError,
  createProvider,
  type UpsertProviderInput,
} from "@/lib/control-plane/client";
import { refuseCrossOrigin } from "@/lib/http/same-origin";

// Server-side proxy for registering a custom provider. Keeps
// CONTROL_PLANE_BASE_URL server-only and attaches the caller's session
// bearer. The control-plane is the authority on permission (platform-admin)
// and on validation (slug charset, env-var name, URL shape); this handler
// only shape-checks and maps the upstream status class to a generic,
// customer-safe message.
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

  const body = await readUpsertBody(request);
  const invalid = upsertValidationError(body);
  if (invalid) {
    return NextResponse.json({ error: invalid }, { status: 400 });
  }

  try {
    const provider = await createProvider(body.input);
    return NextResponse.json(provider, { status: 201 });
  } catch (err) {
    if (err instanceof ControlPlaneError) {
      const status =
        err.status === 400 || err.status === 403 || err.status === 409
          ? err.status
          : 502;
      return NextResponse.json(
        { error: providerErrorMessage(err.status) },
        { status },
      );
    }
    return NextResponse.json(
      { error: "Could not register the provider. Please try again." },
      { status: 500 },
    );
  }
}

interface UpsertBody {
  input: UpsertProviderInput;
}

async function readUpsertBody(request: Request): Promise<UpsertBody> {
  const raw = await request.json().catch((): Record<string, unknown> => ({}));
  const asString = (value: unknown): string =>
    typeof value === "string" ? value : "";
  return {
    input: {
      slug: asString(raw.slug).trim(),
      display_name: asString(raw.display_name).trim(),
      base_url: asString(raw.base_url).trim(),
      api_key_env: asString(raw.api_key_env).trim(),
      litellm_prefix: asString(raw.litellm_prefix).trim(),
      enabled: raw.enabled === true,
    },
  };
}

function upsertValidationError(body: UpsertBody): string | null {
  if (body.input.slug === "") {
    return "A slug is required.";
  }
  if (body.input.api_key_env === "") {
    return "An API key environment variable name is required.";
  }
  return null;
}

// providerErrorMessage maps an upstream status class to a generic,
// customer-safe message. It never forwards raw upstream or internal text.
function providerErrorMessage(status: number): string {
  switch (status) {
    case 400:
      return "That provider could not be validated. Check the slug, key variable name and base URL format.";
    case 403:
      return "You do not have permission to manage providers.";
    case 409:
      return "A provider with that slug already exists.";
    default:
      return "Could not complete the provider request. Please try again.";
  }
}
