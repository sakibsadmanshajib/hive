import { cookies } from "next/headers";

import { createClient } from "@/lib/supabase/server";
import { isJsonObject, parseJsonValue, readObjectField, readResponseText } from "./json";

// isCoworkEnabled reads the authenticated tenant's live feature-gate state
// from edge-api's GET /v1/featuregate (added in #322 for exactly this: a
// Bearer-authenticated end user reading their own gate map, same auth
// selector path as any Supabase-JWT client -- see
// apps/edge-api/internal/auth/selector.go). Server-only --
// EDGE_API_INTERNAL_BASE_URL is the in-compose service DNS name
// (http://edge-api:8080), not exposed to the browser bundle.
//
// Fails closed: any error (network, non-200, missing tenant) hides the
// panel rather than showing it. Matches the Gate.Fetch fail-closed posture
// in apps/edge-api/internal/featuregate/gate.go.
export async function isCoworkEnabled(): Promise<boolean> {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);

  const {
    data: { session },
  } = await supabase.auth.getSession();
  if (!session) {
    return false;
  }

  const baseUrl = process.env.EDGE_API_INTERNAL_BASE_URL;
  if (!baseUrl) {
    return false;
  }

  try {
    const response = await fetch(`${baseUrl}/v1/featuregate`, {
      headers: { Authorization: `Bearer ${session.access_token}` },
      cache: "no-store",
    });
    if (!response.ok) {
      return false;
    }

    const payload = parseJsonValue(await readResponseText(response));
    if (!isJsonObject(payload)) {
      return false;
    }
    const gates = readObjectField(payload, "gates");
    if (!gates) {
      return false;
    }
    return gates["ENABLE_COWORK"] === true;
  } catch {
    return false;
  }
}
