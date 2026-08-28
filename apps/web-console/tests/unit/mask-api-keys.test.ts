import { beforeEach, describe, expect, it } from "vitest";

import { maskLiveApiKeys } from "../e2e/support/mask-api-keys.mjs";

// The console shows a newly created API key in full exactly once, and the
// walkthrough harness screenshots that page into docs/proof/ in a public
// repository. Nothing scans pixels, so this masking pass is the only thing
// between a live key and a permanently published image.

const LIVE_KEY = "hk_" + "A".repeat(20) + "bQ7zx_9-Kd";

describe("maskLiveApiKeys", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });

  it("masks a full-length key rendered on the page", () => {
    document.body.innerHTML = `<code data-testid="created-api-key-secret">${LIVE_KEY}</code>`;
    expect(maskLiveApiKeys(document)).toBe(1);
    expect(document.body.textContent).not.toContain(LIVE_KEY);
    expect(document.body.textContent).toContain("hk_<redacted by capture harness>");
  });

  it("masks a key wherever it renders, not only in the reveal panel", () => {
    document.body.innerHTML = `<div><p>Copied ${LIVE_KEY} to clipboard</p><span>${LIVE_KEY}</span></div>`;
    expect(maskLiveApiKeys(document)).toBe(2);
    expect(document.body.textContent).not.toContain(LIVE_KEY);
  });

  it("masks every occurrence inside one text node", () => {
    document.body.innerHTML = `<p>${LIVE_KEY} then ${LIVE_KEY}</p>`;
    maskLiveApiKeys(document);
    expect(document.body.textContent).not.toContain(LIVE_KEY);
  });

  it("leaves the console's masked list rendering legible", () => {
    document.body.innerHTML = "<td>hk_xxxx•••s2Yn0s</td>";
    expect(maskLiveApiKeys(document)).toBe(0);
    expect(document.body.textContent).toContain("hk_xxxx•••s2Yn0s");
  });

  it("returns zero rather than throwing on a document with no body", () => {
    // A page that has not painted yet, or navigated away mid-run.
    const bodyless = document.implementation.createDocument(null, "root");
    expect(maskLiveApiKeys(bodyless)).toBe(0);
  });
});
