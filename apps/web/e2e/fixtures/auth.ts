import type { APIRequestContext, Page } from "@playwright/test";
import { AUTH_STORAGE_KEY } from "../../src/features/auth/auth-session";

const apiBase = process.env.E2E_API_BASE_URL ?? "http://127.0.0.1:8080";
// Stable prefix keeps test users identifiable for shared-environment cleanup jobs.
const E2E_USER_EMAIL_PREFIX = "e2e_web_smoke";

type AuthResponse = {
  api_key: string;
  user: {
    email: string;
    name?: string;
  };
};

type AuthSessionSeed = {
  apiKey: string;
  email: string;
  name?: string;
};

export async function createSession(request: APIRequestContext) {
  const unique = `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
  const email = `${E2E_USER_EMAIL_PREFIX}_${unique}@example.com`;
  const password = "password123";
  const name = "E2E User";

  const response = await request.post(`${apiBase}/v1/users/register`, {
    data: { email, password, name },
  });

  if (!response.ok()) {
    const body = await response.text();
    throw new Error(`Failed to register e2e user: ${body}`);
  }

  const json = (await response.json()) as AuthResponse;
  return {
    apiKey: json.api_key,
    email: json.user.email,
    name: json.user.name,
    password,
  };
}

export async function seedAuthSession(page: Page, seed: AuthSessionSeed) {
  await page.addInitScript(
    ({ key, session }: { key: string; session: AuthSessionSeed }) => {
      window.localStorage.setItem(key, JSON.stringify(session));
    },
    { key: AUTH_STORAGE_KEY, session: seed },
  );
}

export async function cleanupSessionUser(request: APIRequestContext, apiKey: string): Promise<void> {
  const response = await request.fetch(`${apiBase}/v1/users/me`, {
    method: "DELETE",
    headers: { "x-api-key": apiKey },
  });

  const status = response.status();
  if (status === 200 || status === 204 || status === 404 || status === 405) {
    return;
  }

  const body = await response.text();
  throw new Error(`Unexpected cleanup response (${status}): ${body}`);
}
