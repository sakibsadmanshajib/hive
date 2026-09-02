import { test } from "@playwright/test";
import { execFileSync } from "node:child_process";
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
// execFileSync rather than an import, for the same reason owui.setup.ts spawns
// its two in-container installers: the child is an .mjs module in another app,
// and spawning it needs no declaration file and no allowJs.
test("signed-in chat surface, captured", async () => {
  // The capture waits up to 180 seconds for a provider to finish streaming, on
  // top of a page load and three full-page screenshots. The default 60 second
  // test timeout would cut it off mid-stream and report a Playwright timeout
  // instead of the provider's own failure.
  test.setTimeout(360_000);

  execFileSync(
    "node",
    [path.resolve(__dirname, "..", "capture-chat-proof.mjs")],
    { stdio: "inherit", timeout: 330_000 },
  );
});
