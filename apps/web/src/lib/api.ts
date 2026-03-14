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
  return internalBase && internalBase.length > 0 ? internalBase : getApiBase();
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
