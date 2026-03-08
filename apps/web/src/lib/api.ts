export const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://127.0.0.1:8080";

/**
 * Build standard headers for Hive API calls authenticated via Supabase session.
 */
export function apiHeaders(accessToken: string): Record<string, string> {
    return {
        "content-type": "application/json",
        Authorization: `Bearer ${accessToken}`,
    };
}
