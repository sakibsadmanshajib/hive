import { test } from "@playwright/test";
import { spawn } from "node:child_process";
import path from "node:path";

// Visual proof of the signed-in chat surface, run as a spec so that the session
// it needs arrives the way every other Open WebUI run gets one: the `owui-proof`
// project depends on `owui-setup`, which walks the real "Continue with Hive"
// journey and saves the storage state.
//
// WHY THIS IS A SPEC WRAPPING A SCRIPT, RATHER THAN EITHER ONE ALONE
//
// The capture itself stays a standalone Node script. It imports stamp.mjs, the
// one implementation of the proof footer this repository stamps into an image,
// which lives in apps/agent-console and is untyped; and it writes into
// docs/proof/, because a Playwright HTML reporter CLEARS its own output
// directory before writing and silently deleted a whole capture pass on PR #951.
//
// But a workflow step that ran the script directly had to invoke the setup
// project on its own to get a session first, and tools/verify-spec-wiring.mjs
// rejects that by design: setup files are deliberately excluded from its spec
// universe, so an invocation selecting only `owui-setup` selects zero specs and
// reads as a broken selector. It is right to reject it. This shape gives the
// guard a real spec to see, gives the capture its session through the ordinary
// dependency mechanism, and leaves the script runnable by hand.
//
// A spawned child rather than an import, for the same reason owui.setup.ts
// spawns its two in-container installers: the child is an .mjs module in
// another app, and running it needs no declaration file and no allowJs.
//
// Awaited spawn, never the sync form. A synchronous child blocks the Playwright
// worker's event loop, which stops the timeout below from firing at all while
// the capture runs, and PR #838 found a test recorded as passed at 30194 ms
// under a 30000 ms timeout for exactly that reason
// (tools/lint-no-sync-child-process-in-tests.mjs). stdio is inherited so the
// capture log reaches the run log live, which is where a failure is read.
test("signed-in chat surface, captured", async () => {
  // The capture waits up to 180 seconds for a provider to finish streaming, on
  // top of a page load and three full-page screenshots. The default 60 second
  // test timeout would cut it off mid-stream and report a Playwright timeout
  // instead of the provider's own failure.
  test.setTimeout(360_000);

  const script = path.resolve(__dirname, "..", "capture-chat-proof.mjs");
  await new Promise<void>((resolve, reject) => {
    const child = spawn("node", [script], { stdio: "inherit" });
    child.on("error", reject);
    child.on("close", (code, signal) => {
      if (code === 0) resolve();
      else reject(new Error(`capture-chat-proof.mjs exited with code ${code} signal ${signal}`));
    });
  });
});
