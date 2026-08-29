/**
 * ci-web-e2e-secret-free.test.ts
 *
 * The two browser jobs in ci.yml that stand up their own full stack must
 * reference no repository secret at all.
 *
 * The bug this guards: a workflow run triggered by Dependabot is handed the
 * Dependabot secret store, never the Actions one, so every `secrets.*`
 * expression in it resolves to the empty string with no warning anywhere. On
 * 2026-08-25 `Web E2E (full stack)`, a required context on main, was failing
 * on ten open dependency pull requests at once for exactly that reason:
 * `S3_ENDPOINT` and its four siblings arrived empty, control-plane exited at
 * boot with `storage unavailable: missing S3_ENDPOINT, S3_ACCESS_KEY,
 * S3_SECRET_KEY, S3_REGION`, the compose stack never came up, and the whole
 * dependency-update queue sat blocked behind a check none of those pull
 * requests could have broken. Behind it stood a second wall: the two fixture
 * passwords were secrets too, and would have thrown by name a few steps later.
 *
 * Both jobs boot their own Postgres, GoTrue, PostgREST, Redis and LiteLLM, so
 * neither has any business holding a credential. Reading a secret into either
 * one is what makes a Dependabot run behave differently from every other run,
 * and the difference shows up as a stack that will not start rather than as
 * anything naming the actual cause. Asserting the absence is cheap, runs in
 * the required unit check with no network, and fails at review time on the
 * pull request that adds the secret.
 *
 * A job that genuinely needs live credentials (`live-integration`, the
 * deploy jobs) is not covered here and should not be: those already skip
 * themselves rather than run degraded.
 *
 * See issues #820 and #821.
 */

import { describe, it, expect } from "vitest";
import {
  blockForDisplayName,
  isComment,
  jobBlocks,
  readCiWorkflow,
} from "./support/ci-workflow";

// The display names, not the YAML keys: the display name is what branch
// protection lists as a required context, so it is the stable handle.
const SECRET_FREE_JOBS = [
  "Web E2E (full stack)",
  "Interaction coverage (console controls)",
];

describe("ci.yml browser jobs are secret-free", () => {
  const source = readCiWorkflow();
  const blocks = jobBlocks(source);

  it("finds the jobs it is asserting about", () => {
    expect(blocks.size).toBeGreaterThan(5);
  });

  for (const displayName of SECRET_FREE_JOBS) {
    it(`${displayName} reads no repository secret`, () => {
      const { key, lines } = blockForDisplayName(blocks, displayName);
      const offenders = lines
        .map((line, index) => ({ line, index }))
        .filter(({ line }) => /\bsecrets\./.test(line) && !isComment(line))
        .map(({ line }) => line.trim());

      expect(
        offenders,
        `job \`${key}\` (${displayName}) reads ${offenders.length} ` +
          "repository secret(s). A Dependabot-triggered run receives none of " +
          "them and gets the empty string instead, which boots a broken " +
          "stack rather than failing by name. This job stands up everything " +
          "it needs; stub the value or generate it in a step, the way the " +
          "S3 block and the fixture passwords already are."
      ).toEqual([]);
    });
  }
});
