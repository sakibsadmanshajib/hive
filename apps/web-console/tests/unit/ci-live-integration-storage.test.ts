/**
 * ci-live-integration-storage.test.ts
 *
 * The `live-integration` job must exercise a real object store it owns, and
 * must never take its S3 configuration from a repository secret again.
 *
 * The bug this guards, issue #1324: the `S3_ENDPOINT` secret was last written
 * on 2026-04-21, four months before the self-hosted Supabase cutover, and
 * still named the Supabase Cloud project this repository left. That project no
 * longer resolves in DNS, so every `POST /v1/files` in this job died at the
 * socket. The Files and Batches conformance assertions were then carried as
 * `it.fails` markers, which meant the suite reported green for four months
 * while covering nothing, and the same blind 500 reached production without
 * CI ever going red (issue #1282).
 *
 * There is no correct value to rotate that secret to, so the job now stands up
 * its own throwaway `supabase/storage-api` the same way it already stands up
 * its own Postgres. `deploy/docker/Caddyfile.supabase` serves `/storage/v1` on
 * the in-network listener only and states that ports 80 and 443 must never be
 * exposed, so the box's Storage is unreachable from a hosted runner by design
 * and re-pointing the secret is not an option that exists.
 *
 * Both halves are asserted, because either one alone rots into the same
 * silence. A secret coming back means the job dials a dead host again; the
 * fixture step going away means it dials nothing at all, and the conformance
 * suites would then fail for a reason nobody would connect to this change.
 *
 * Issue #1380 read `ci.yml`'s `http://127.0.0.1:9` stub as this coverage. That
 * stub is in the two Playwright jobs, which run no SDK suite. It is guarded by
 * ci-web-e2e-secret-free.test.ts and is deliberately left alone.
 */

import { describe, it, expect } from "vitest";
import {
  blockForDisplayName,
  isComment,
  jobBlocks,
  readCiWorkflow,
} from "./support/ci-workflow";

// The display name, not the YAML key, for the same reason the sibling guard
// gives: the display name is what branch protection lists.
const JOB = "Live integration (SDK tests + smoke)";

// The fixture script. Named as a literal rather than matched loosely, so
// renaming or deleting the script fails this test instead of silently
// satisfying a regex that no longer describes anything.
const FIXTURE_SCRIPT = "scripts/ci-object-store.sh";

describe("ci.yml live-integration owns its object store", () => {
  const blocks = jobBlocks(readCiWorkflow());

  it("finds the job it is asserting about", () => {
    expect(blocks.size).toBeGreaterThan(5);
  });

  it("reads no S3 repository secret", () => {
    const { key, lines } = blockForDisplayName(blocks, JOB);
    const offenders = lines
      .map((line) => line)
      .filter((line) => /\bsecrets\.S3_/.test(line) && !isComment(line))
      .map((line) => line.trim());

    expect(
      offenders,
      `job \`${key}\` (${JOB}) reads ${offenders.length} S3 repository ` +
        "secret(s). That secret names a Supabase Cloud project deleted in " +
        "August 2026 and cannot be repointed at the self-hosted box, whose " +
        "Storage is on an in-network listener that must not be exposed. " +
        `Take the values from ${FIXTURE_SCRIPT} instead, which is what the ` +
        "step below the throwaway-database step already does."
    ).toEqual([]);
  });

  it("stands up a real object store fixture", () => {
    const { key, lines } = blockForDisplayName(blocks, JOB);
    const invokes = lines.filter(
      (line) => line.includes(FIXTURE_SCRIPT) && !isComment(line)
    );

    expect(
      invokes.length,
      `job \`${key}\` (${JOB}) never invokes ${FIXTURE_SCRIPT}. Without it ` +
        "S3_ENDPOINT is unset, edge-api and control-plane refuse to boot, " +
        "and the Files and Batches conformance suites fail for a reason " +
        "that has nothing to do with the gateway. This job is the only one " +
        "in ci.yml that exercises the object storage path at all."
    ).toBeGreaterThan(0);
  });

  it("keeps the S3 values out of the job-level env block", () => {
    const { lines } = blockForDisplayName(blocks, JOB);
    // The job-level env block is everything before the first `steps:` key.
    const stepsAt = lines.findIndex((line) => /^ {4}steps:\s*$/.test(line));
    expect(stepsAt).toBeGreaterThan(0);
    const envLines = lines.slice(0, stepsAt);
    const offenders = envLines
      .filter((line) => /^ {6}S3_[A-Z_]+:/.test(line) && !isComment(line))
      .map((line) => line.trim());

    expect(
      offenders,
      "these S3_* entries are at job level, where they take precedence over " +
        "what the fixture step writes to $GITHUB_ENV. That is the exact trap " +
        "the SUPABASE_* and HIVE_API_KEY blocks in this job were removed to " +
        "avoid: the step succeeds, the value is ignored, and the stack talks " +
        "to whatever the job-level entry named. Let the step write them."
    ).toEqual([]);
  });
});
