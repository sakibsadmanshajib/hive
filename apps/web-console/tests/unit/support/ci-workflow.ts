/**
 * ci-workflow.ts
 *
 * Shared helpers for the unit tests that assert properties of
 * .github/workflows/ci.yml.
 *
 * Deliberately no YAML parser. These read the same bytes a reviewer reads, so
 * an assertion cannot pass against a normalised document that differs from
 * what is actually in the file, and the tests carry no dependency of their
 * own. Extracted from ci-web-e2e-secret-free.test.ts when a second workflow
 * guard needed the same slicing.
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));
export const REPO_ROOT = resolve(HERE, "../../../../..");
export const CI_WORKFLOW = resolve(REPO_ROOT, ".github/workflows/ci.yml");

export function readCiWorkflow(): string {
  return readFileSync(CI_WORKFLOW, "utf8");
}

/**
 * Slices ci.yml into top-level job blocks. A job starts at a two-space-indented
 * key and runs until the next one.
 */
export function jobBlocks(source: string): Map<string, string[]> {
  const lines = source.split("\n");
  const blocks = new Map<string, string[]>();
  let current: string[] | null = null;
  let inJobs = false;

  for (const line of lines) {
    if (/^jobs:\s*$/.test(line)) {
      inJobs = true;
      continue;
    }
    if (!inJobs) continue;
    // A key back at column zero ends the jobs mapping.
    if (/^\S/.test(line)) break;
    const jobKey = /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(line);
    if (jobKey) {
      current = [];
      blocks.set(jobKey[1], current);
      continue;
    }
    current?.push(line);
  }
  return blocks;
}

export function blockForDisplayName(
  blocks: Map<string, string[]>,
  displayName: string
): { key: string; lines: string[] } {
  for (const [key, lines] of blocks) {
    if (lines.some((l) => l.trim() === `name: ${displayName}`)) {
      return { key, lines };
    }
  }
  throw new Error(
    `no job in .github/workflows/ci.yml declares \`name: ${displayName}\`. ` +
      "If the job was renamed, rename it here too; if it was deleted, it was " +
      "a required context and its deletion needs saying out loud."
  );
}

export function isComment(line: string): boolean {
  return line.trim().startsWith("#");
}
