// Masks live Hive API keys in a rendered page before a screenshot is taken.
//
// A capture harness screenshots the console and commits the PNG under
// docs/proof/ in a public repository. `npm run lint:proof-tokens` reads text
// files only, GitHub secret scanning never sees a release asset, and nothing
// at all inspects pixels, so a key rendered on screen at the moment of the
// shutter has no automated backstop whatsoever (.claude/rules/orchestrator.md,
// PR #578). Masking has to happen before the capture, in the page.
//
// `generateSecret` (apps/control-plane/internal/apikeys/service.go) emits
// "hk_" plus 43 base64url characters. The console's list view renders a
// revoked or stored key as "hk_xxxx" plus bullets plus a six character
// suffix, which is far short of the floor below and stays legible in the
// screenshot; only a full, usable secret is masked.

/**
 * Rewrites every text node under `document.body` that contains a full-length
 * API key. Runs inside the browser via `page.evaluate`, so it must stay
 * self-contained: no imports, no closure over module scope.
 *
 * @param {Document} doc the live document, defaults to the page's own
 * @returns {number} how many text nodes were rewritten
 */
export function maskLiveApiKeys(doc = globalThis.document) {
  const root = doc?.body;
  if (!root) return 0;
  const walker = doc.createTreeWalker(root, 4 /* NodeFilter.SHOW_TEXT */);
  let masked = 0;
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.nodeValue ?? "";
    // Fresh regex per node: a shared /g literal carries lastIndex between
    // calls and would skip every other match.
    const next = text.replace(/hk_[A-Za-z0-9_-]{24,}/g, "hk_<redacted by capture harness>");
    if (next !== text) {
      node.nodeValue = next;
      masked += 1;
    }
  }
  return masked;
}
