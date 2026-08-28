#!/usr/bin/env node
// Deploy path-filter coverage guard.
//
// deploy-demo-box.yml's `on.push.paths` is a hand-maintained allowlist that
// decides whether a merge to main triggers a real deploy. Twice now a real
// build input had no entry: apps/web-console (issue behind PR #786, its own
// comment block in that workflow) and vendor/open-webui (the fork's source
// tree, added by PR #938). Both times the merge was green, the change sat on
// main, and nothing outside a manual audit noticed it never shipped -- a
// paths filter with a missing entry does not fail loud, it just never runs.
//
// This script closes the class rather than the one instance: it reads every
// COPY/ADD source in every deploy/docker/Dockerfile.* and checks it against
// the paths filter. Deliberately scoped to deploy/docker/Dockerfile.* rather
// than only the Dockerfiles the demo-box deploy job actually builds: every
// Dockerfile in that directory COPYs exclusively from apps/**, packages/**,
// deploy/docker/** or go.work(.sum) today, so checking the wider set costs
// nothing extra and still catches the next vendored tree the moment its
// Dockerfile lands, before anyone wires it into a compose profile.
//
// It does not (and cannot, without executing the compose profile logic)
// check docker-compose.yml's own inputs (env files, bind mounts, image
// digests) or the workflow's own script/scripts: entries -- those are listed
// by hand in the paths filter today and are not COPY sources. On the filter
// side it only understands the two glob shapes this filter actually uses: an
// exact literal ('go.work') and a directory wildcard ('apps/edge-api/**'). A
// third shape showing up in the filter would need this matcher extended.
//
// A Docker COPY source can itself carry a `*` (`go.work.sum*`, meaning "copy
// this file if it exists"). Docker resolves that against the real build
// context, so this script does the same rather than string-matching the
// glob literally: it expands the wildcard against the actual files on disk
// and checks each real match. Comparing the un-expanded glob text against
// the filter would have made `go.work.sum*` equal to the literal filter
// entry `go.work.sum` by construction, silently approving a future
// `go.work.sum.bak` that the real Docker build would also pick up and that
// the filter does not cover (CodeRabbit review, PR #976).
//
// Run: node .github/ci/lint-deploy-paths-filter.mjs

import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { parse } from 'yaml';

// Exported (not just used below) so list-covered-deploy-changes.mjs can ask this file's
// exact question -- "does the paths filter cover this file" -- rather than
// re-deriving its own copy of the glob rules.
export const WORKFLOW_FILE = '.github/workflows/deploy-demo-box.yml';
const DOCKER_DIR = 'deploy/docker';

// Same YAML-1.1-boolean-key dodge as lint-workflow-check-names.mjs: `on:`
// parses to the boolean `true` under the default schema.
function onBlock(workflow) {
  return workflow?.on ?? workflow?.[true] ?? workflow?.True;
}

export function pushPaths(workflow) {
  const on = onBlock(workflow);
  const paths = on?.push?.paths;
  return Array.isArray(paths) ? paths : [];
}

export function isCovered(source, filters) {
  return filters.some((pattern) => {
    if (pattern.endsWith('/**')) {
      const prefix = pattern.slice(0, -3);
      return source === prefix || source.startsWith(`${prefix}/`);
    }
    return source === pattern;
  });
}

function escapeRegExpLiteral(text) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// Expands one COPY/ADD source token against the real repository tree. A
// plain path (no `*`) is returned as-is, trailing slash stripped so a
// directory copy compares equal to the filter's `/**`-stripped prefix. A
// wildcard is resolved the way Docker resolves it: only within the final
// path segment, against whatever actually exists in that directory today. A
// wildcard matching nothing (the "copy if present" idiom, and the file is
// not present) expands to nothing, same as Docker would copy nothing.
function expandSource(rawSrc) {
  const src = rawSrc.replace(/\/+$/, '');
  if (!src.includes('*')) return [src];

  const dir = dirname(src);
  const base = dir === '.' ? src : src.slice(dir.length + 1);
  const pattern = new RegExp(`^${base.split('*').map(escapeRegExpLiteral).join('.*')}$`);
  const prefix = dir === '.' ? '' : `${dir}/`;

  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return [];
  }
  return entries.filter((e) => pattern.test(e)).map((e) => `${prefix}${e}`);
}

// Extracts COPY/ADD host-filesystem sources from one Dockerfile's text.
// Joins backslash line continuations first, then reads instruction lines.
// Skips `--from=<stage>` copies: those read a previous build stage's
// filesystem, not the host, so they carry no path-filter obligation of
// their own (their ultimate host source, if any, is COPYed into that
// earlier stage and is covered by this same scan).
function copySources(text) {
  const joined = text.replace(/\\\r?\n/g, ' ');
  const sources = [];
  for (const rawLine of joined.split('\n')) {
    const line = rawLine.trim();
    const match = /^(?:COPY|ADD)\s+(.*)$/i.exec(line);
    if (!match) continue;
    const tokens = match[1].split(/\s+/).filter(Boolean);
    if (tokens.some((t) => t.startsWith('--from='))) continue;
    const args = tokens.filter((t) => !t.startsWith('--'));
    // COPY/ADD needs at least one source and one destination.
    if (args.length < 2) continue;
    for (const src of args.slice(0, -1)) sources.push(...expandSource(src));
  }
  return sources;
}

// Gated on direct execution, not just `import`: list-covered-deploy-changes.mjs
// imports pushPaths and isCovered from this file to stay in step with it, and
// an import must not also run the full Dockerfile scan and print its verdict
// to stdout as a side effect -- that would corrupt the covered-changed-paths
// list that caller builds from its own stdout.
if (import.meta.url === `file://${process.argv[1]}`) {
  const workflow = parse(readFileSync(WORKFLOW_FILE, 'utf8'));
  const filters = pushPaths(workflow);

  if (filters.length === 0) {
    console.error(`${WORKFLOW_FILE}: could not find on.push.paths -- refusing to pass silently.`);
    process.exit(1);
  }

  const dockerfiles = readdirSync(DOCKER_DIR).filter((f) => f.startsWith('Dockerfile.'));
  const uncovered = new Map(); // source -> Set of dockerfiles

  for (const file of dockerfiles) {
    const path = join(DOCKER_DIR, file);
    const sources = copySources(readFileSync(path, 'utf8'));
    for (const source of sources) {
      if (isCovered(source, filters)) continue;
      if (!uncovered.has(source)) uncovered.set(source, new Set());
      uncovered.get(source).add(path);
    }
  }

  if (uncovered.size > 0) {
    console.error(`${WORKFLOW_FILE} paths filter is missing entries:`);
    for (const [source, files] of uncovered) {
      console.error(`  - ${source} (COPYed by ${[...files].join(', ')})`);
    }
    console.error(
      'Add a paths entry for each (a literal for an exact file, or "<dir>/**" for a directory) so a ' +
        'change here still triggers a demo-box deploy. See this file\'s own header comment for why this ' +
        'has already happened twice silently.',
    );
    process.exit(1);
  }

  console.log(
    `Deploy path-filter coverage OK: every COPY/ADD source across ${dockerfiles.length} Dockerfiles ` +
      `under ${DOCKER_DIR}/ is covered by ${WORKFLOW_FILE}'s push.paths.`,
  );
}
