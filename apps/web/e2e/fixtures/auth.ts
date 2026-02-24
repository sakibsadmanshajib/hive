import type { APIRequestContext, Page } from "@playwright/test";

const apiBase = process.env.E2E_API_BASE_URL ?? "http://127.0.0.1:8080";

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
  const email = `e2e_${unique}@example.com`;
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
  await page.addInitScript((session) => {
    window.localStorage.setItem("bdai.auth.session", JSON.stringify(session));
  }, seed);
}
