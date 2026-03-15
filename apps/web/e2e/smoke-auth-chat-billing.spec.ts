import { expect, type Page, test } from "@playwright/test";

import { createSession, seedAuthSession } from "./fixtures/auth";

async function mockBrowserSignup(page: Page) {
  await page.route("**/auth/v1/signup", async (route) => {
    const method = route.request().method();
    if (method === "OPTIONS") {
      await route.fulfill({
        status: 204,
        headers: {
          "access-control-allow-origin": "*",
          "access-control-allow-methods": "POST, OPTIONS",
          "access-control-allow-headers": "*",
        },
        body: "",
      });
      return;
    }

    const payload = route.request().postDataJSON() as { email?: string; options?: { data?: { name?: string } } } | null;
    if (!payload?.email) {
      await route.fulfill({
        status: 400,
        contentType: "application/json",
        headers: { "access-control-allow-origin": "*" },
        body: JSON.stringify({ error: "Expected signup email in request payload" }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { "access-control-allow-origin": "*" },
      body: JSON.stringify({
        access_token: "e2e_mock_access_token",
        token_type: "bearer",
        expires_in: 3600,
        refresh_token: "e2e_mock_refresh_token",
        user: {
          id: "e2e_mock_user_id",
          email: payload.email,
          user_metadata: { name: payload?.options?.data?.name ?? "E2E UI User" },
        },
      }),
    });
  });
}

test("unauthenticated root stays in guest mode, guest chat works, and locked paid models open a dismissible auth modal", async ({ page }) => {
  test.setTimeout(180000);
  await page.goto("/");

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText("Guest mode is active. Sign in to unlock paid models and top up credits.")).toBeVisible();
  await expect(page.getByText("Guest mode only supports free models.")).toBeVisible();
  const messageArticles = page.locator("article");
  await expect(messageArticles).toHaveCount(1);

  await page.getByPlaceholder("Ask something...").fill("hello from guest smoke");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(messageArticles).toHaveCount(3, { timeout: 120000 });
  await expect(messageArticles.nth(1)).toContainText("hello from guest smoke");
  await expect(messageArticles.nth(2)).toContainText("Assistant");

  await page.getByRole("combobox", { name: "Model" }).click();
  const paidOption = page.getByRole("option", { name: /fast-chat/i });
  await expect(paidOption).toContainText("Requires account and credits");
  await paidOption.click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("Unlock paid models")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(page.getByText("Guest mode is active. Sign in to unlock paid models and top up credits.")).toBeVisible();
});

test("registering from the locked-model modal unlocks paid models in place", async ({ page }) => {
  const unique = `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
  const email = `e2e_web_smoke_modal_${unique}@example.com`;

  await mockBrowserSignup(page);
  await page.goto("/");

  await page.getByRole("combobox", { name: "Model" }).click();
  await page.getByRole("option", { name: /fast-chat/i }).click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await dialog.locator("#register-name").fill("E2E Modal User");
  await dialog.locator("#register-email").fill(email);
  await dialog.locator("#register-password").fill("password123");
  await dialog.getByRole("button", { name: "Create account" }).scrollIntoViewIfNeeded();
  await dialog.getByRole("button", { name: "Create account" }).click();

  await expect(page).toHaveURL(/\/$/);
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/guest mode is active/i)).toBeHidden();
  await expect(page.getByRole("button", { name: "Open profile menu" })).toBeVisible();

  const modelPicker = page.getByRole("combobox", { name: "Model" });
  await modelPicker.click();
  await page.getByRole("option", { name: /fast-chat/i }).click();
  await expect(modelPicker).toContainText("fast-chat");
});

test("chat success and failure messaging", async ({ page, request }) => {
  const session = await createSession(request);
  await seedAuthSession(page, {
    accessToken: session.accessToken,
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
  await seedAuthSession(page, {
    accessToken: session.accessToken,
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
