// Self-check: POST malformed JSON to the stub and confirm it answers 400
// instead of crashing the process. Run directly:
//
//   node apps/agent-console/proof/harness/stub-server.test.mjs
//
// ponytail: one assert-based script, no test framework. This exists to prove
// the fix for the unguarded JSON.parse in stub-server.mjs (POST /__control
// and POST /v1/agent/tasks); it is not a general test suite for the stub.

import { spawn } from "node:child_process";
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const PORT = Number(process.env.STUB_PORT || 4099);
const ORIGIN = `http://127.0.0.1:${PORT}`;

function waitForUp(timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolvePromise, reject) => {
    (async function poll() {
      try {
        const res = await fetch(`${ORIGIN}/__control`);
        if (res.ok) return resolvePromise();
      } catch {
        // not up yet
      }
      if (Date.now() > deadline) return reject(new Error("stub did not come up in time"));
      setTimeout(poll, 100);
    })();
  });
}

async function postMalformed(path) {
  return fetch(`${ORIGIN}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{not valid json",
  });
}

async function main() {
  const child = spawn(process.execPath, [join(HERE, "stub-server.mjs")], {
    env: { ...process.env, STUB_PORT: String(PORT) },
    stdio: ["ignore", "inherit", "inherit"],
  });

  try {
    await waitForUp(5000);

    const response = await postMalformed("/__control");
    assert.equal(
      response.status,
      400,
      `expected 400 for malformed JSON on /__control, got ${response.status}`,
    );

    // Prove the process is still alive and serving, not merely that this one
    // request happened to return something before the process died.
    const followUp = await fetch(`${ORIGIN}/__control`);
    assert.equal(followUp.status, 200, "stub did not survive the malformed /__control request");

    const tasksResponse = await postMalformed("/v1/agent/tasks");
    assert.equal(
      tasksResponse.status,
      400,
      `expected 400 for malformed JSON on /v1/agent/tasks, got ${tasksResponse.status}`,
    );

    const finalCheck = await fetch(`${ORIGIN}/__control`);
    assert.equal(
      finalCheck.status,
      200,
      "stub did not survive the malformed /v1/agent/tasks request",
    );

    console.log("[stub-server.test] PASS: malformed JSON returns 400 on both routes, stub stays up");
  } finally {
    child.kill("SIGTERM");
  }
}

main().catch((err) => {
  console.error("[stub-server.test] FAIL:", err.message);
  process.exit(1);
});
