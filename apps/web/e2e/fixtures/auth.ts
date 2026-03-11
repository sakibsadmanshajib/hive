import type { APIRequestContext, Page } from "@playwright/test";
import { AUTH_STORAGE_KEY } from "../../src/features/auth/auth-session";

const supabaseUrl = process.env.E2E_SUPABASE_URL ?? "http://127.0.0.1:54321";
const supabaseAnonKey = process.env.E2E_SUPABASE_ANON_KEY;
const allowDevTokenFallback = process.env.E2E_ALLOW_DEV_TOKEN_FALLBACK === "true";

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
 * If explicitly enabled for smoke-only environments, it can fall back to a
 * synthetic session token when Supabase signup is unavailable.
 */
export async function createSession(request: APIRequestContext) {
  const unique = `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
  const email = `${E2E_USER_EMAIL_PREFIX}_${unique}@example.com`;
  const password = "password1234";

  try {
    if (!supabaseAnonKey) {
      throw new Error("E2E_SUPABASE_ANON_KEY is required for Supabase signup");
    }

    const response = await request.fetch(`${supabaseUrl}/auth/v1/signup`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        apikey: supabaseAnonKey,
      },
      data: { email, password, data: { name: "E2E User" } },
    });

    if (!response.ok()) {
      throw new Error(`Supabase signup failed with status ${response.status()}`);
    }

    const json = (await response.json()) as {
      access_token?: string;
      user?: { id?: string };
    };
    const accessToken = json.access_token;
    if (accessToken && typeof accessToken === "string") {
      return { accessToken, email, name: "E2E User", userId: json.user?.id };
    }

    throw new Error("Supabase signup did not return an access token");
  } catch (error) {
    if (!allowDevTokenFallback) {
      throw error;
    }
  }

  return { accessToken: `e2e_fallback_${unique}`, email, name: "E2E User" };
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
