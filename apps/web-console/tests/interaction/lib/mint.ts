// What the gate's sign-in actually decides, separated from Playwright so it
// can be tested in the required unit job.
//
// This is the file behind the incident the whole suite exists to answer: the
// setup reported success in 13 milliseconds having minted nothing, and the only
// symptom was an ENOENT three hundred lines away. Its regression protection was
// runtime-only, which is to say it had none: nothing outside a live browser run
// could notice it breaking again. The two decisions worth guarding are here,
// and auth.setup.ts is now a shell around them.

/** The command that mints a session, as argv. */
export function mintCommand(
  email: string,
  targetUrl: string,
  statePath: string,
): { command: string; args: string[] } {
  if (email.trim() === "") {
    throw new Error(
      "INTERACTION_EMAIL (or E2E_VERIFIED_EMAIL) must name an existing account; the gate cannot enumerate authenticated routes without a session",
    );
  }
  // The command line form of tests/e2e/support/live-auth.mjs, not an import of
  // it. An extensionless import resolves to the synchronous .ts wrapper for the
  // type checker and to the asynchronous .mjs at run time, which is exactly how
  // an unawaited promise came to pass for a session; and naming the .mjs
  // directly fails under Playwright's CommonJS output with "exports is not
  // defined". A child process has neither problem, and keeps the service role
  // key out of this worker's module graph.
  return {
    command: "node",
    args: ["tests/e2e/support/live-auth.mjs", email, targetUrl, statePath],
  };
}

/** Turns a child-process failure into a message that names the real cause. */
export function mintFailure(error: unknown): Error {
  // live-auth.mjs redacts its own output, so relaying it is safe.
  const streams =
    typeof error === "object" && error !== null
      ? [
          "stdout" in error ? String(error.stdout ?? "") : "",
          "stderr" in error ? String(error.stderr ?? "") : "",
        ].join("")
      : String(error);
  return new Error(`[live-auth] mint failed\n${streams}`);
}

/**
 * The mint throws on every failure it can see. This is the one it cannot: a
 * helper that returns without having written the file. Failing here names the
 * cause, where leaving it to the sweep's own `newContext` produces an ENOENT
 * that reads like a harness bug.
 */
export function assertStateWritten(exists: boolean, statePath: string): void {
  if (!exists) {
    throw new Error(
      `live-auth returned without writing ${statePath}; the sweep has no session and must not run`,
    );
  }
}
