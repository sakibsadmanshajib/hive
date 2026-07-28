import { execFileSync } from "node:child_process";
import { E2E_VERIFIED_EMAIL } from "./e2e-auth-creds";

// Spec-side entry to the fixture CLI's `reset-profile` action.
//
// Shells out rather than importing the seeder: Playwright's transform compiles
// spec imports to CommonJS, and importing a .mjs from a spec makes Node
// evaluate that CJS output in ES module scope ("exports is not defined"). The
// child process also keeps this identical to the full reset the beforeEach
// hooks already run, and keeps the service-role key out of the Playwright
// worker's own module graph.
export function resetProfileBetweenSpecs(testInfo?: { email?: string }): void {
  const targetEmail = testInfo?.email ?? E2E_VERIFIED_EMAIL;
  try {
    execFileSync(
      "node",
      ["tests/e2e/support/e2e-auth-fixtures.mjs", "reset-profile", targetEmail],
      {
        cwd: process.cwd(),
        env: { ...process.env, NODE_OPTIONS: "" },
        stdio: "pipe",
      }
    );
  } catch (err: unknown) {
    const e = err as { stdout?: Buffer; stderr?: Buffer };
    process.stderr.write(
      `[e2e-auth-fixtures] reset-profile failed\n${e.stdout ?? ""}${e.stderr ?? ""}\n`
    );
    throw err;
  }
}
