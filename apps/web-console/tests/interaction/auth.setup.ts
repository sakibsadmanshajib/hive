// Signs the interaction coverage gate in once and saves the session.
//
// Uses the shared live-auth helper (docs/live-test-auth.md), which mints a
// session the way a magic link login does. It needs no password, so this gate
// cannot become another of the runs that broke concurrent agents by rotating a
// shared account's credentials. INTERACTION_EMAIL names an existing account;
// nothing about that account is created or modified.

import { test as setup } from "@playwright/test";

import { writeStorageState } from "../e2e/support/live-auth";
import { AUTH_STATE_FILE, interactionBaseUrl, interactionEmail } from "./lib/config";

setup("authenticate", () => {
  const email = interactionEmail();
  if (email === "") {
    throw new Error(
      "INTERACTION_EMAIL (or E2E_VERIFIED_EMAIL) must name an existing account; the gate cannot enumerate authenticated routes without a session",
    );
  }
  writeStorageState(
    { email, targetUrl: new URL("/console", interactionBaseUrl()).toString() },
    AUTH_STATE_FILE,
  );
});
