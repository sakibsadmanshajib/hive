function requirePublicEnv(name: string, value: string | undefined): string {
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

export function getApiBase(): string {
  return requirePublicEnv("NEXT_PUBLIC_API_BASE_URL", process.env.NEXT_PUBLIC_API_BASE_URL);
}

export function getServerApiBase(): string {
  const internalBase = process.env.INTERNAL_API_BASE_URL?.trim();
  if (!internalBase || internalBase.length === 0) {
    return getApiBase();
  }

  const normalized = internalBase.toLowerCase();
  return normalized === "undefined" ? getApiBase() : internalBase;
}

export function getAppUrl(path: string): string {
  if (typeof window === "undefined") {
    return path;
  }
  return new URL(path, window.location.origin).toString();
}

/**
 * Build standard headers for Hive API calls authenticated via Supabase session.
 */
export function apiHeaders(accessToken: string): Record<string, string> {
  return {
    "content-type": "application/json",
    Authorization: `Bearer ${accessToken}`,
  };
}

/**
 * Parse response body as JSON only when content-type is JSON. Avoids "Unexpected token '<'" when
 * the server returns HTML (e.g. 500 error page). Returns a result object so callers can handle
 * non-JSON or error responses without throwing.
 */
export async function parseJsonResponse(response: Response): Promise<
  { ok: true; data: unknown } | { ok: false; error: string; status: number }
> {
  const contentType = response.headers.get("content-type") ?? "";
  const isJson = contentType.includes("application/json");
  const text = await response.text();
  if (!isJson) {
    const message = response.ok ? "Response was not JSON" : `Request failed (${response.status})`;
    return { ok: false, error: message, status: response.status };
  }
  if (!text.trim()) {
    return response.ok ? { ok: true, data: {} } : { ok: false, error: "Empty response", status: response.status };
  }
  try {
    const data = JSON.parse(text) as unknown;
    if (!response.ok) {
      const err = data && typeof data === "object" && "error" in data && typeof (data as { error: unknown }).error === "string"
        ? (data as { error: string }).error
        : "Request failed";
      return { ok: false, error: err, status: response.status };
    }
    return { ok: true, data };
  } catch {
    return { ok: false, error: "Invalid JSON response", status: response.status };
  }
}
