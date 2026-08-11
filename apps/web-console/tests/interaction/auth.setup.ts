// Signs the interaction coverage gate in once and saves the session.
//
// Uses the shared live-auth helper (docs/live-test-auth.md), which mints a
// session the way a magic link login does. It needs no password, so this gate
// cannot become another of the runs that broke concurrent agents by rotating a
// shared account's credentials. INTERACTION_EMAIL names an existing account;
// nothing about that account is created or modified.
//
// WHY IT SHELLS OUT INSTEAD OF IMPORTING
// --------------------------------------
// Two modules answer to the name `../e2e/support/live-auth`: the `.mjs` that
// implements the mint, and the `.ts` wrapper that shells out to it. An
// extensionless import resolves to the `.ts` for the type checker and to the
// `.mjs` at run time, so the call was checked against a synchronous signature
// while invoking an asynchronous one. The returned promise floated, the mint
// never happened, and the setup reported success in 13 milliseconds having
// written nothing (CI run 31445547804). The sweep then died on a missing
// storage state file, which is how a gate that had never measured a single
// control still looked like it was running. Naming the `.mjs` explicitly does
// not fix it either: Playwright compiles specs to CommonJS and evaluating that
// output in ES module scope fails with "exports is not defined".
//
// So: the documented command line form. No resolution ambiguity, a
// non-zero exit is impossible to ignore, and the service role key stays in a
// child process rather than entering this worker's module graph.

import { execFileSync } from "node:child_process";
import { existsSync, rmSync } from "node:fs";

import { test as setup } from "@playwright/test";

import { AUTH_STATE_FILE, interactionBaseUrl, interactionEmail } from "./lib/config";

setup("authenticate", () => {
  const email = interactionEmail();
  if (email === "") {
    throw new Error(
      "INTERACTION_EMAIL (or E2E_VERIFIED_EMAIL) must name an existing account; the gate cannot enumerate authenticated routes without a session",
    );
  }

  // A storage state left behind by an earlier run would let a failed mint pass
  // for a successful one, and would sign the sweep in as whoever ran last.
  rmSync(AUTH_STATE_FILE, { force: true });

  const targetUrl = new URL("/console", interactionBaseUrl()).toString();
  try {
    execFileSync(
      "node",
      ["tests/e2e/support/live-auth.mjs", email, targetUrl, AUTH_STATE_FILE],
      { cwd: process.cwd(), env: { ...process.env, NODE_OPTIONS: "" }, stdio: "pipe" },
    );
  } catch (error: unknown) {
    // live-auth.mjs redacts its own output, so relaying it is safe.
    const streams =
      typeof error === "object" && error !== null
        ? [
            "stdout" in error ? String(error.stdout ?? "") : "",
            "stderr" in error ? String(error.stderr ?? "") : "",
          ].join("")
        : String(error);
    throw new Error(`[live-auth] mint failed\n${streams}`);
  }

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
