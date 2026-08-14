// The checks that keep the coverage gate honest, none of which need a live
// deployment or a session.
//
// They are in their own file, and their own Playwright project, because they
// used to sit inside the live sweep behind the same credential gate: with no
// CHAT_URL and no Supabase keys the config matched zero files, so the one test
// that proves the detector can tell a working control from a broken one never
// ran anywhere except on a machine already able to run the full sweep. A gate
// nobody has watched fail is worth nothing.
import { test, expect } from "@playwright/test";

declare global {
  interface Window {
    /** Set by the fixture's destructive button if anything ever clicks it. */
    __destructiveFired?: boolean;
  }
}

import { EXCLUSIONS, REGISTRY } from "./data";
import {
  checkWithoutFiring,
  enumerate,
  exclusionFailures,
  expiredExclusions,
  isDeferred,
  proveByClick,
  sampleChatter,
} from "./lib";

const SELF_TEST_HTML = `
<main style="min-height:400px">
  <div id="out">idle</div>
  <div id="toast" role="alert"></div>
  <button aria-label="Wired">Wired</button>
  <button aria-label="Dead">Dead</button>
  <button aria-label="Failing">Failing</button>
  <button aria-label="Toasty">Toasty</button>
  <button aria-label="Pollster">Pollster</button>
  <button aria-label="Hanging">Hanging</button>
  <button aria-label="Cut Off">Cut Off</button>
  <button aria-label="Delete Everything">Delete Everything</button>
  <button aria-label="Locked" title="finish the form first" disabled>Locked</button>
</main>
<script>
  const out = document.getElementById('out');
  const toast = document.getElementById('toast');
  const at = (n) => document.querySelector('[aria-label="' + n + '"]');
  at('Wired').addEventListener('click', () => { out.textContent = 'the wired button did something'; });
  at('Failing').addEventListener('click', async () => {
    try { await fetch('/api/save', { method: 'POST' }); } catch (e) {}
    toast.textContent = 'Something went wrong, please try again';
  });
  at('Toasty').addEventListener('click', () => { toast.textContent = 'Error: could not save'; });
  // Records its own activation, so the test can prove the harness never fired
  // it rather than trusting the verdict string.
  window.__destructiveFired = false;
  at('Delete Everything').addEventListener('click', () => { window.__destructiveFired = true; });
  // Sends a request nothing ever answers. The old rule proved a control on the
  // request alone, so a button wired to a hanging endpoint was covered for ever.
  at('Hanging').addEventListener('click', () => { fetch('/api/hang').catch(() => {}); });
  // Its request dies in transport, which emits requestfailed and no response.
  at('Cut Off').addEventListener('click', () => { fetch('/api/cut').catch(() => {}); });
  // Polls on its own, exactly like Open WebUI does, and its button does
  // nothing. Without the idle sample the poll lands inside the settle window
  // and is counted as this button's network proof.
  setInterval(() => { fetch('/api/poll').catch(() => {}); }, 300);
</script>
`;

test.describe("chat coverage gate self-checks", () => {
  test("the prover tells a working control from a broken one", async ({ page }) => {
    test.setTimeout(120_000);
    // Five buttons that look alike: wired does something and must be proven;
    // dead has no handler and must be unproven; failing calls an endpoint that
    // answers 500 and shows a toast and must be unproven, which is the case
    // that used to come back proven twice over; toasty only shows an error and
    // must be unproven; pollster does nothing at all while the page polls
    // underneath it, and must be unproven despite the traffic.
    //
    // An intercepted origin rather than setContent, so the failing button has a
    // same-origin endpoint whose status the prover can actually read.
    await page.route("https://cov.selftest/**", async (route) => {
      const url = route.request().url();
      if (url.endsWith("/index")) {
        await route.fulfill({ contentType: "text/html", body: SELF_TEST_HTML });
        return;
      }
      if (url.endsWith("/api/poll")) {
        await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
        return;
      }
      if (url.endsWith("/api/hang")) {
        // Never fulfilled and never aborted: the request stays open past the
        // settle window, exactly like an endpoint that accepts and hangs.
        return;
      }
      if (url.endsWith("/api/cut")) {
        await route.abort("connectionreset");
        return;
      }
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ detail: "boom" }),
      });
    });
    await page.goto("https://cov.selftest/index");

    const controls = await enumerate(page, "self-test");
    const byName = (name: string) => {
      const found = controls.find((c) => c.name === name);
      expect(found, "the fixture must render " + name).toBeTruthy();
      if (!found) throw new Error("unreachable: the assertion above already failed");
      return found;
    };

    // What the page does with nobody touching it, the same sample the sweep
    // takes per surface.
    const chatter = await sampleChatter(page, 1500);
    expect(
      [...chatter].some((signature) => signature.endsWith("/api/poll")),
      "the idle sample must see the fixture's own polling, or it is not sampling anything",
    ).toBe(true);

    const wired = await proveByClick(page, byName("Wired"), { ignoreRequests: chatter });
    const dead = await proveByClick(page, byName("Dead"), { ignoreRequests: chatter });
    const failing = await proveByClick(page, byName("Failing"), { ignoreRequests: chatter });
    const toasty = await proveByClick(page, byName("Toasty"), { ignoreRequests: chatter });
    const pollster = await proveByClick(page, byName("Pollster"), { ignoreRequests: chatter });

    expect(wired.proven, "a working control was reported as broken: " + wired.detail).toBe(true);
    expect(dead.proven, "a control with no handler was reported as working: " + dead.detail).toBe(
      false,
    );
    expect(
      failing.proven,
      "a control whose endpoint answered 500 was reported as working: " + failing.detail,
    ).toBe(false);
    expect(failing.detail).toContain("500");
    expect(
      toasty.proven,
      "a control that only raised an error was reported as working: " + toasty.detail,
    ).toBe(false);
    expect(toasty.detail).toContain("raised an error");
    expect(
      pollster.proven,
      "a dead control was proven by the page's own background polling: " + pollster.detail,
    ).toBe(false);

    const hanging = await proveByClick(page, byName("Hanging"), { ignoreRequests: chatter });
    expect(
      hanging.proven,
      "a control whose request nothing answered was reported as working: " + hanging.detail,
    ).toBe(false);
    expect(hanging.detail).toContain("nothing answered");

    const cutOff = await proveByClick(page, byName("Cut Off"), { ignoreRequests: chatter });
    expect(
      cutOff.proven,
      "a control whose request died in transport was reported as working: " + cutOff.detail,
    ).toBe(false);
    expect(cutOff.detail).toContain("never reached a server");

    // Destructive and disabled controls are checked, not fired, and the
    // verdict is never proof. Both used to count as coverage: the disabled one
    // outright, which meant a broken control could be "fixed" by disabling it.
    const destructive = await checkWithoutFiring(page, byName("Delete Everything"));
    expect(destructive.proven, "a destructive control was reported as proven").toBe(false);
    expect(destructive.proof).toBe("not-fired");
    expect(isDeferred(destructive)).toBe(true);
    // The verdict string is not the claim being tested. The claim is that the
    // control was never activated, and only the page can answer that.
    expect(
      await page.evaluate(() => window.__destructiveFired === true),
      "the harness fired a destructive control it reported as not fired",
    ).toBe(false);

    const locked = await proveByClick(page, byName("Locked"), { ignoreRequests: chatter });
    expect(locked.proven, "a disabled control was reported as proven: " + locked.detail).toBe(
      false,
    );
    expect(locked.proof).toBe("not-fired");
    expect(locked.detail).toContain("finish the form first");
  });

  test("every inert-registry entry carries a justification", async () => {
    const bad = REGISTRY.filter((e) => !e.key || !e.justification.trim());
    expect(
      bad.map((e) => e.key || "(no key)"),
      "registry entries without a key or a justification",
    ).toEqual([]);
    expect(exclusionFailures(EXCLUSIONS), "malformed surface exclusions").toEqual([]);
  });

  // An exclusion ends when the thing blocking it is fixed, and the only
  // authority on that is the issue tracker. Every path through this test that
  // cannot read that state now fails. It used to skip on a missing token and
  // `continue` past any non-ok response, which meant the expiry could never
  // fire: an exclusion would outlive its own issue forever and the check would
  // report green the whole time.
  test("no excluded surface waits on an issue that already closed", async ({ request }) => {
    const pending = EXCLUSIONS.filter((entry) => entry.issue !== undefined);
    if (pending.length === 0) return;

    const token = process.env.GITHUB_TOKEN ?? process.env.GH_TOKEN ?? "";
    expect(
      token,
      "GITHUB_TOKEN (or GH_TOKEN) is required: this check reads whether the issues that " +
        `justify ${pending.length} surface exclusion(s) are still open, and it fails rather ` +
        "than passes when it cannot find out",
    ).not.toBe("");

    const repo = process.env.GITHUB_REPOSITORY ?? "sakibsadmanshajib/hive";
    const closed = new Set<number>();
    const unreadable: string[] = [];
    for (const entry of pending) {
      const url = "https://api.github.com/repos/" + repo + "/issues/" + String(entry.issue);
      const response = await request.get(url, {
        headers: { authorization: "Bearer " + token, accept: "application/vnd.github+json" },
      });
      if (!response.ok()) {
        unreadable.push(`#${String(entry.issue)}: HTTP ${response.status()}`);
        continue;
      }
      const body: unknown = await response.json();
      const state =
        typeof body === "object" && body !== null
          ? new Map(Object.entries(body)).get("state")
          : undefined;
      if (state === "closed" && entry.issue !== undefined) closed.add(entry.issue);
    }

    expect(
      unreadable,
      "issue state could not be read, so an exclusion whose reason has expired would go unnoticed",
    ).toEqual([]);
    expect(expiredExclusions(EXCLUSIONS, closed)).toEqual([]);
  });
});
