// Signs the coverage sweep in and saves storageState.
//
// signInToChat lives in tests/e2e/support/live-auth (PR #810 plus the chat hop
// this suite contributed back): it mints a session the way a magic link does,
// so no password is read, written or rotated, then carries it through the real
// "Continue with Hive" OIDC hop, which is the only thing that gets Open WebUI's
// own session cookie set. See docs/live-test-auth.md.
import path from "node:path";

import { test as setup } from "@playwright/test";

import { signInToChat } from "../../tests/e2e/support/live-auth";

const STATE = path.join(__dirname, ".auth", "state.json");
const CHAT = process.env.CHAT_URL ?? process.env.OWUI_URL ?? "";
const CONSOLE = process.env.CONSOLE_URL ?? "";
const EMAIL = process.env.OWUI_E2E_EMAIL ?? process.env.HIVE_QA_AGENT_EMAIL ?? "";

setup("authenticate against the chat surface", async ({ browser }) => {
  setup.setTimeout(6 * 60_000);
  if (!CHAT || !CONSOLE || !EMAIL) {
    setup.skip(true, "CHAT_URL, CONSOLE_URL and an account are all required");
    return;
  }

  const context = await browser.newContext({ viewport: { width: 1440, height: 950 } });
  await signInToChat(context, { email: EMAIL, consoleUrl: CONSOLE, chatUrl: CHAT });
  await context.storageState({ path: STATE });
  await context.close();
});
