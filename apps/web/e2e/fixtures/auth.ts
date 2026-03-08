import type { APIRequestContext, Page } from "@playwright/test";
import { AUTH_STORAGE_KEY } from "../../src/features/auth/auth-session";

const supabaseUrl = process.env.E2E_SUPABASE_URL ?? "http://127.0.0.1:54321";
const supabaseAnonKey =
  process.env.E2E_SUPABASE_ANON_KEY ??
  "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6ImFub24iLCJleHAiOjE5ODM4MTI5OTZ9.CRXP1A7WOeoJeXxjNni43kdQwgnWNReilDMblYTn_I0";

const E2E_USER_EMAIL_PREFIX = "e2e_web_smoke";

type AuthSessionSeed = {
  accessToken: string;
  email: string;
  name?: string;
};

/**
 * Create a real Supabase user for E2E testing.
 *
 * Calls Supabase Auth REST signup to create a user and get an access token.
 * If Supabase is unavailable (e.g. minimal CI), falls back to a dev API key
 * prefix which the API accepts when ALLOW_DEV_API_KEY_PREFIX=true.
 */
export async function createSession(request: APIRequestContext) {
  const unique = `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
  const email = `${E2E_USER_EMAIL_PREFIX}_${unique}@example.com`;
  const password = "password1234";

  try {
    const response = await request.fetch(`${supabaseUrl}/auth/v1/signup`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        apikey: supabaseAnonKey,
      },
      data: { email, password, data: { name: "E2E User" } },
    });

    if (response.ok()) {
      const json = await response.json();
      const accessToken = json.access_token;
      if (accessToken && typeof accessToken === "string") {
        return { accessToken, email, name: "E2E User", userId: json.user?.id };
      }
    }
  } catch {
    // Supabase unavailable — fall through to dev key fallback
  }

  // Fallback: use dev API key prefix (requires ALLOW_DEV_API_KEY_PREFIX=true)
  const devToken = `dev-user-${unique}`;
  return { accessToken: devToken, email, name: "E2E User" };
}

export async function seedAuthSession(page: Page, seed: AuthSessionSeed) {
  await page.addInitScript(
    ({ key, session }: { key: string; session: AuthSessionSeed }) => {
      window.localStorage.setItem(key, JSON.stringify(session));
    },
    { key: AUTH_STORAGE_KEY, session: seed },
  );
}

export async function cleanupSessionUser(_request: APIRequestContext, _userId?: string): Promise<void> {
  // Supabase admin cleanup would require the service role key.
  // In CI, test users are ephemeral and the database is reset between runs.
}
