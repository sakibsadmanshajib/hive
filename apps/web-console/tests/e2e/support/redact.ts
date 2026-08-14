// One redactor for anything a live test writes down.
//
// A capture of a live run reaches a log, a ledger, a CI artifact and a pull
// request, and every auth flow that carries its credential in the URL (the
// OAuth callback, a magic link, an invitation accept) puts a working
// credential in the middle of an otherwise boring string. PR #578 published
// four live invite tokens exactly that way.
//
// A .ts rather than an .mjs on purpose: Playwright compiles spec imports to
// CommonJS and cannot load an .mjs from a spec, which is why redactSecrets in
// e2e-fixture-seed.mjs (the same job for the mint's own output) cannot be
// reused here.

/**
 * A parameter name carrying a credential. Substring matching, not exact:
 * an exact list missed `id_token` and `provider_token`, which are as much a
 * login as `access_token` is.
 */
const CREDENTIAL_NAME = /(token|code|state|otp|secret|password|passwd|jwt|apikey|api_key)/i;

function scrubQuery(search: string): string {
  const params = new URLSearchParams(search);
  let touched = false;
  for (const name of [...params.keys()]) {
    if (CREDENTIAL_NAME.test(name)) {
      params.set(name, "REDACTED");
      touched = true;
    }
  }
  return touched ? params.toString() : search;
}

/**
 * Strips credential-bearing parameters out of one URL, in the query string
 * and in the fragment, including the hash-router shape (`#/route?code=...`)
 * where the credential sits inside the fragment's own query.
 */
export function redactUrl(url: string): string {
  try {
    const parsed = new URL(url);
    parsed.search = scrubQuery(parsed.search.replace(/^\?/, ""));
    const hash = parsed.hash.replace(/^#/, "");
    if (hash.includes("=")) {
      const cut = hash.indexOf("?");
      parsed.hash =
        cut === -1
          ? "#" + scrubQuery(hash)
          : "#" + hash.slice(0, cut) + "?" + scrubQuery(hash.slice(cut + 1));
    }
    return parsed.toString();
  } catch {
    return redactText(url);
  }
}

/**
 * The same job for free text: an error message, a stack, a framework-generated
 * report. Playwright writes the URLs it navigated through into a
 * `waitForURL` timeout, so the sign-in hop's own failure message enumerates
 * the OAuth callback with a live `code` and `state` in it, and no per-URL
 * redaction reaches that text because the framework produced it.
 */
export function redactText(text: string): string {
  return text
    .replace(
      /([?&#][A-Za-z0-9_.-]*(?:token|code|state|otp|secret|password|passwd|jwt|api[_-]?key)[A-Za-z0-9_.-]*=)[^&#\s"'<>]+/gi,
      "$1REDACTED",
    )
    .replace(/\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}/g, "REDACTED_JWT");
}
