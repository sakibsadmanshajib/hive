// Content-lint Astro integration.
//
// Registers on the astro:config:setup hook (which fires for both dev and build,
// before any content processing) and scans every MDX page under
// src/content/pages. It enforces the compliance lints that the zod schema
// cannot express because they are about prose and cross-file references, not
// frontmatter shape. Any violation throws and fails the build.
//
// Lints (all ERROR, fail build):
//   1. Banned strings: "150x" and "up to 150x" anywhere, including comments.
//   2. Sovereignty wording only in scope:onprem pages.
//   3. The Pinterest proof point only in cost-scoped sections (FILE-level
//      heuristic, see the caveat on that lint below).
//   4. Every <Claim id="..."> has a matching frontmatter sources[].claim_id.
//
// The frontmatter parse here is a deliberately small YAML reader for the
// fields we lint (scope, sources[].claim_id). It is not a general YAML parser;
// page authors must keep those fields in the simple shapes the schema defines.
import type { AstroIntegration } from 'astro';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

interface Violation {
  file: string;
  rule: string;
  detail: string;
}

// Phrases that signal sovereignty / data-residency framing. Legitimate only in
// scope:onprem pages. Kept narrow to avoid false positives on neutral words.
const SOVEREIGNTY_MARKERS = [
  'data never leaves',
  'never leaves your',
  'stays on your hardware',
  'inside the network boundary',
  'inside your network boundary',
  'data residency',
  'data-residency',
  'data sovereignty',
  'data-sovereign',
  'no data leaves',
];

// The Pinterest proof point. Allowed only in cost-scoped sections, marked with
// the `cost-section` sentinel comment on a page.
//
// We detect the actual proof CLAIM, not the bare word "Pinterest", so neutral
// mentions in meta-comments, nav, or footer copy do not trip the lint. The
// claim is "Pinterest" co-occurring with one of its signature cost figures
// (Qwen3-VL, the ~90 percent cost cut, or the ~30 percent accuracy lift).
const PINTEREST_NAME = 'pinterest';
const PINTEREST_CLAIM_SIGNATURES = ['qwen', '90 percent', '30 percent'];
const COST_SECTION_SENTINEL = 'cost-section';

const BANNED_STRINGS = ['up to 150x', '150x'];

function walkMdx(dir: string): string[] {
  let out: string[] = [];
  let entries: string[] = [];
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out = out.concat(walkMdx(full));
    } else if (/\.(md|mdx)$/.test(entry) && !entry.startsWith('_')) {
      out.push(full);
    }
  }
  return out;
}

function splitFrontmatter(raw: string): { frontmatter: string; body: string } {
  const match = raw.match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/);
  if (!match) return { frontmatter: '', body: raw };
  return { frontmatter: match[1], body: match[2] };
}

function parseScope(frontmatter: string): string | null {
  const m = frontmatter.match(/^\s*scope:\s*['"]?(\w+)['"]?\s*$/m);
  return m ? m[1] : null;
}

// Extract every claim_id under a sources: block. Small and shape-specific.
function parseSourceClaimIds(frontmatter: string): Set<string> {
  const ids = new Set<string>();
  const re = /claim_id:\s*['"]?([^'"\n]+)['"]?/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(frontmatter)) !== null) {
    ids.add(m[1].trim());
  }
  return ids;
}

function findClaimIds(body: string): string[] {
  const ids: string[] = [];
  const re = /<Claim\s+[^>]*\bid=["']([^"']+)["']/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(body)) !== null) {
    ids.push(m[1].trim());
  }
  return ids;
}

function lintFile(file: string, label: string): Violation[] {
  const raw = readFileSync(file, 'utf8');
  const lower = raw.toLowerCase();
  const violations: Violation[] = [];
  const { frontmatter, body } = splitFrontmatter(raw);
  const scope = parseScope(frontmatter);

  // 1. Banned strings, anywhere including comments.
  for (const banned of BANNED_STRINGS) {
    if (lower.includes(banned)) {
      violations.push({
        file: label,
        rule: 'banned-string',
        detail: `Found banned string "${banned}". The approved cost framing is "~10x to ~30x cheaper". Remove it everywhere, including comments.`,
      });
      break;
    }
  }

  // 2. Sovereignty wording only where onprem scope is in play.
  //    scope:onprem and scope:both both contain a legitimate onprem context, so
  //    both may carry sovereignty wording. Only scope:cloud (a cloud-only page)
  //    must never claim sovereignty or no-egress.
  if (scope !== 'onprem' && scope !== 'both') {
    for (const marker of SOVEREIGNTY_MARKERS) {
      if (lower.includes(marker)) {
        violations.push({
          file: label,
          rule: 'sovereignty-scope',
          detail: `Sovereignty wording ("${marker}") appears in a scope:${scope ?? 'unknown'} page. Sovereignty / no-egress language is only legitimate where onprem scope is in play (scope:onprem or scope:both).`,
        });
        break;
      }
    }
  }

  // 3. Pinterest proof point only in cost-scoped sections.
  //    Trip only on the actual claim (Pinterest + a signature cost figure), not
  //    a bare mention, and only when no cost-section sentinel is present.
  //
  //    CAVEAT (file-level heuristic, not section-scoped): this checks only that
  //    the cost-section sentinel exists SOMEWHERE in the same file as the
  //    Pinterest claim. It cannot verify the claim actually sits inside that
  //    section, nor that it is non-adjacent to a sovereignty claim elsewhere on
  //    the page. A page that carries both a cost section and a sovereignty claim
  //    could place the Pinterest proof point next to the sovereignty claim and
  //    still pass. Until this is upgraded to a true section-scoped check, the
  //    SPEC placement rule (section 10: cost-proof and residency-risk components
  //    never share a section band) requires a MANUAL adjacency review gate
  //    before publish. Do not treat a green lint as proof of correct placement.
  const hasPinterestClaim =
    lower.includes(PINTEREST_NAME) &&
    PINTEREST_CLAIM_SIGNATURES.some((sig) => lower.includes(sig));
  if (hasPinterestClaim && !lower.includes(COST_SECTION_SENTINEL)) {
    violations.push({
      file: label,
      rule: 'pinterest-scope',
      detail:
        'The Pinterest proof point appears outside a cost-scoped section. It may only run in a cost section (mark the section with an HTML comment containing "cost-section") and never adjacent to a sovereignty claim or on the Security page.',
    });
  }

  // 4. Every <Claim id> has a matching frontmatter sources[].claim_id.
  const sourceIds = parseSourceClaimIds(frontmatter);
  for (const claimId of findClaimIds(body)) {
    if (!sourceIds.has(claimId)) {
      violations.push({
        file: label,
        rule: 'claim-source-crosscheck',
        detail: `<Claim id="${claimId}"> has no matching sources[].claim_id in frontmatter. Every rendered claim must be backed by a citation. TODO: when remark wiring lands this check moves into the MDX pipeline; until then this build-start scan is authoritative.`,
      });
    }
  }

  return violations;
}

export default function contentLint(): AstroIntegration {
  return {
    name: 'hive-content-lint',
    hooks: {
      'astro:config:setup': ({ config, logger }) => {
        const pagesDir = fileURLToPath(new URL('content/pages', config.srcDir));
        const files = walkMdx(pagesDir);
        const all: Violation[] = [];
        for (const f of files) {
          const label = relative(fileURLToPath(config.root), f);
          all.push(...lintFile(f, label));
        }
        if (all.length > 0) {
          for (const v of all) {
            logger.error(`[${v.rule}] ${v.file}: ${v.detail}`);
          }
          throw new Error(
            `Content lint failed with ${all.length} violation(s). Fix the compliance issues above before building.`,
          );
        }
        logger.info(`Content lint passed (${files.length} page(s) scanned).`);
      },
    },
  };
}
