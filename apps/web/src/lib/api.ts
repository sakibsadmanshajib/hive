function requirePublicEnv(name: string, value: string | undefined): string {
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

export function getApiBase(): string {
  return requirePublicEnv("NEXT_PUBLIC_API_BASE_URL", process.env.NEXT_PUBLIC_API_BASE_URL);
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
