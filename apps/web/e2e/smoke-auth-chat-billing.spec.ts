import { expect, test } from "@playwright/test";

import { cleanupSessionUser, createSession, seedAuthSession } from "./fixtures/auth";

const createdSessionApiKeys: string[] = [];

test.afterEach(async ({ request }) => {
  while (createdSessionApiKeys.length > 0) {
    const apiKey = createdSessionApiKeys.pop();
    if (!apiKey) {
      continue;
    }
    await cleanupSessionUser(request, apiKey);
  }
});

test("unauthenticated root redirects to auth", async ({ page }) => {
  await page.goto("/");

  await expect(page).toHaveURL(/\/auth$/);
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
});

test("register happy path reaches chat workspace", async ({ page }) => {
  const unique = `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
  const email = `e2e_web_smoke_ui_${unique}@example.com`;
  const registerForm = page.locator("form").filter({ has: page.getByRole("button", { name: "Create account" }) });
  const corsHeaders = {
    "access-control-allow-origin": "*",
    "access-control-allow-methods": "POST, OPTIONS",
    "access-control-allow-headers": "content-type",
  };

  await page.route("**/v1/users/register", async (route) => {
    const method = route.request().method();
    if (method === "OPTIONS") {
      await route.fulfill({ status: 204, headers: corsHeaders, body: "" });
      return;
    }

    const payload = route.request().postDataJSON() as { email?: string; name?: string } | null;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: corsHeaders,
      body: JSON.stringify({
        api_key: "sk_e2e_register_mock",
        user: {
          email: payload?.email ?? email,
          name: payload?.name ?? "E2E UI User",
        },
      }),
    });
  });

  await page.goto("/auth");
  await registerForm.getByPlaceholder("Name").fill("E2E UI User");
  await registerForm.getByPlaceholder("Email").fill(email);
  await registerForm.getByPlaceholder("Password").fill("password123");
  await registerForm.getByRole("button", { name: "Create account" }).click();

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("button", { name: "Open profile menu" })).toBeVisible();
  await expect(page.getByPlaceholder("Ask something...")).toBeVisible();
});

test("chat success and failure messaging", async ({ page, request }) => {
  const session = await createSession(request);
  createdSessionApiKeys.push(session.apiKey);
  await seedAuthSession(page, {
    apiKey: session.apiKey,
    email: session.email,
    name: session.name,
  });

  await page.route("**/v1/chat/completions", async (route) => {
    const payload = route.request().postDataJSON() as { messages?: Array<{ content?: string }> };
    const prompt = payload.messages?.at(-1)?.content ?? "";

    if (prompt.includes("fail")) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Chat backend unavailable" }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        id: "chatcmpl_e2e",
        object: "chat.completion",
        model: "mock-chat-model",
        choices: [
          {
            index: 0,
            finish_reason: "stop",
            message: { role: "assistant", content: "Mocked assistant reply" },
          },
        ],
      }),
    });
  });

  await page.goto("/");
  await page.getByPlaceholder("Ask something...").fill("say hello");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByText("Mocked assistant reply")).toBeVisible();

  await page.getByPlaceholder("Ask something...").fill("please fail now");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByRole("main").getByText("Chat backend unavailable").first()).toBeVisible();
});

test("billing access from profile menu and top-up failure messaging", async ({ page, request }) => {
  const session = await createSession(request);
  createdSessionApiKeys.push(session.apiKey);
  await seedAuthSession(page, {
    apiKey: session.apiKey,
    email: session.email,
    name: session.name,
  });

  await page.route("**/v1/payments/intents", async (route) => {
    await route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: "Could not create payment intent" }),
    });
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Open profile menu" }).click();
  await page.getByRole("menuitem", { name: "Billing" }).click();

  await expect(page).toHaveURL(/\/billing$/);
  await expect(page.getByRole("heading", { name: "Billing moved to Settings" })).toBeVisible();

  await page.getByRole("link", { name: "Open Settings" }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await page.getByRole("button", { name: "Top up now" }).click();
  await expect(page.getByText("Could not create payment intent")).toBeVisible();
});
