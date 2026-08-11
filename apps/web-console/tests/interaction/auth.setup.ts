// Signs the interaction coverage gate in once and saves the session.
//
// Uses the shared live-auth helper (docs/live-test-auth.md), which mints a
// session the way a magic link login does. It needs no password, so this gate
// cannot become another of the runs that broke concurrent agents by rotating a
// shared account's credentials. INTERACTION_EMAIL names an existing account;
// nothing about that account is created or modified.
//
// The `.mjs` extension is deliberate and load bearing. `../e2e/support/live-auth`
// without it resolves to this same module anyway, but as an untyped import: the
// call then type checked against the `.ts` wrapper's synchronous signature while
// actually invoking the asynchronous one here. The returned promise floated, the
// mint never happened, and the setup reported success in 13 milliseconds having
// written nothing (CI run 31445547804). The sweep then died on a missing storage
// state file, which is how a gate that had never measured a single control still
// looked like it was running.

import { existsSync, rmSync } from "node:fs";

import { test as setup } from "@playwright/test";

import { writeStorageState } from "../e2e/support/live-auth.mjs";
import { AUTH_STATE_FILE, interactionBaseUrl, interactionEmail } from "./lib/config";

setup("authenticate", async () => {
  const email = interactionEmail();
  if (email === "") {
    throw new Error(
      "INTERACTION_EMAIL (or E2E_VERIFIED_EMAIL) must name an existing account; the gate cannot enumerate authenticated routes without a session",
    );
  }

  // A storage state left behind by an earlier run would let a failed mint pass
  // for a successful one, and would sign the sweep in as whoever ran last.
  rmSync(AUTH_STATE_FILE, { force: true });

  await writeStorageState({
    email,
    targetUrl: new URL("/console", interactionBaseUrl()).toString(),
    statePath: AUTH_STATE_FILE,
  });

  // The mint throws on every failure it can see, so this catches the one it
  // cannot: a helper that returns without having written the file. Failing here
  // names the cause. Leaving it to the sweep's own `newContext` produces an
  // ENOENT three hundred lines away that reads like a harness bug.
  if (!existsSync(AUTH_STATE_FILE)) {
    throw new Error(
      `live-auth returned without writing ${AUTH_STATE_FILE}; the sweep has no session and must not run`,
    );
  }
});
