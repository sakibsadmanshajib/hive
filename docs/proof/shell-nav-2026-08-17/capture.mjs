// Visual proof for the Hive shell change.
//
// Two images built from the same pinned Open WebUI digest, differing only in
// this branch's diff, captured side by side in both themes, with the DOM read
// as well as the pixels. Same method as PR #909.
//
// Run inside mcr.microsoft.com/playwright, with the two Open WebUI containers
// and the Caddy proxy already up on the same docker network.

import { chromium } from "playwright";
import { mkdir, writeFile } from "node:fs/promises";

const OUT = process.env.OUT_DIR ?? "/out";
const BEFORE = process.env.BEFORE_URL ?? "http://owui-before:8080";
const AFTER = process.env.AFTER_URL ?? "http://caddy-proof:80";

const VIEWPORT = { width: 1440, height: 900 };

const notes = [];
const log = (line) => {
  notes.push(line);
  console.log(line);
};

/** Open WebUI reads localStorage before it boots: theme, and its session token. */
const seed = (theme, sidebar = 'true') => `
  try {
    localStorage.setItem('theme', ${JSON.stringify(theme)});
    // Open WebUI persists its own expanded/collapsed state here and defaults a
    // fresh profile to collapsed. Both sides of the comparison are seeded the
    // same way, so the difference in the capture is the change and not a
    // remembered preference.
    localStorage.setItem('sidebar', ${JSON.stringify(sidebar)});
    // The old launcher only injects itself when a session token is present.
    // Seeding one is the launcher's own gate, not a change to the product:
    // without it the baseline capture would omit the very control this change
    // replaces, which would make the comparison flattering rather than honest.
    if (!localStorage.getItem('token')) localStorage.setItem('token', 'proof');
  } catch (e) {}
`;

async function settle(page) {
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(1500);
}

async function shot(context, url, theme, file, { path = "/", collapse = false } = {}) {
  const page = await context.newPage();
  await page.addInitScript(seed(theme, collapse ? "false" : "true"));
  await page.goto(url + path, { waitUntil: "domcontentloaded" });
  await settle(page);

  await page.screenshot({ path: `${OUT}/${file}`, fullPage: false });
  log(`captured ${file} <- ${url}${path} (${theme})`);
  return page;
}

async function readShell(page) {
  return page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll("[data-hive-nav]"))
      .filter((el) => el.offsetParent !== null)
      .map((el) => {
        const style = getComputedStyle(el);
        const box = el.getBoundingClientRect();
        return {
          id: el.getAttribute("data-hive-nav"),
          text: (el.textContent || "").trim(),
          href: el.getAttribute("href"),
          ariaCurrent: el.getAttribute("aria-current"),
          ariaLabel: el.getAttribute("aria-label"),
          height: Math.round(box.height),
          width: Math.round(box.width),
          colour: style.color,
          font: style.fontFamily,
          transition: style.transitionDuration,
        };
      });

    const body = getComputedStyle(document.body);
    return {
      pathname: location.pathname,
      title: document.title,
      navRows: rows,
      launcherCount: document.querySelectorAll("#hive-agent-launcher").length,
      sidebarPresent: Boolean(document.getElementById("sidebar")),
      agentPanel: (() => {
        const frame = document.querySelector('iframe[title="Agent workspace"]');
        if (!frame) return null;
        const box = frame.getBoundingClientRect();
        return {
          src: frame.getAttribute("src"),
          width: Math.round(box.width),
          height: Math.round(box.height),
          sameOrigin: (() => {
            try {
              return Boolean(frame.contentDocument);
            } catch {
              return false;
            }
          })(),
          embeddedFlag: (() => {
            try {
              return frame.contentDocument.querySelectorAll('[data-hv-embedded="1"]').length;
            } catch {
              return -1;
            }
          })(),
          themeFlag: (() => {
            try {
              const el = frame.contentDocument.querySelector("[data-hv-theme]");
              return el ? el.getAttribute("data-hv-theme") : null;
            } catch {
              return null;
            }
          })(),
        };
      })(),
      bodyBackground: body.backgroundColor,
      bodyFont: body.fontFamily,
      hankenLoaded: Array.from(document.fonts)
        .map((f) => f.family)
        .includes("Hanken Grotesk"),
    };
  });
}

async function main() {
  await mkdir(OUT, { recursive: true });
  const browser = await chromium.launch();
  const results = {};

  for (const theme of ["light", "dark"]) {
    const context = await browser.newContext({ viewport: VIEWPORT });

    const before = await shot(
      context,
      BEFORE,
      theme,
      `01-before-chat-${theme}.png`,
    );
    results[`before-${theme}`] = await readShell(before);
    await before.close();

    const after = await shot(context, AFTER, theme, `02-after-chat-${theme}.png`);
    results[`after-${theme}`] = await readShell(after);
    await after.close();

    const agents = await shot(
      context,
      AFTER,
      theme,
      `03-after-agents-${theme}.png`,
      { path: "/agents" },
    );
    results[`after-agents-${theme}`] = await readShell(agents);
    await agents.close();

    await context.close();
  }

  // Collapsed rail: every destination survives, labels do not.
  const railContext = await browser.newContext({ viewport: VIEWPORT });
  const rail = await shot(railContext, AFTER, "light", "04-after-rail-light.png", {
    collapse: true,
  });
  results["after-rail"] = await readShell(rail);
  await rail.close();
  await railContext.close();

  // Keyboard focus on the agent row, so the two ring treatment is on the record.
  try {
  const focusContext = await browser.newContext({ viewport: VIEWPORT });
  const focusPage = await focusContext.newPage();
  await focusPage.addInitScript(seed("light"));
  await focusPage.goto(AFTER + "/", { waitUntil: "domcontentloaded" });
  await settle(focusPage);
  const agentRow = focusPage.locator('[data-hive-nav="agents"]').first();
  await agentRow.focus();
  await focusPage.waitForTimeout(200);
  results["focus"] = await agentRow.evaluate((el) => ({
    boxShadow: getComputedStyle(el).boxShadow,
    outline: getComputedStyle(el).outlineStyle,
  }));
  await focusPage.screenshot({ path: `${OUT}/05-after-focus-ring.png` });
  log("captured 05-after-focus-ring.png");
  await focusContext.close();

  // Reduced motion: the hover transition is removed rather than shortened.
  const rmContext = await browser.newContext({
    viewport: VIEWPORT,
    reducedMotion: "reduce",
  });
  const rmPage = await rmContext.newPage();
  await rmPage.addInitScript(seed("light"));
  await rmPage.goto(AFTER + "/", { waitUntil: "domcontentloaded" });
  await settle(rmPage);
  results["reducedMotion"] = await rmPage
    .locator('[data-hive-nav="agents"]')
    .first()
    .evaluate((el) => getComputedStyle(el).transitionDuration);
  await rmContext.close();

  // Coarse pointer: the row grows to the touch floor instead of overlapping
  // its neighbour with an invisible hit area.
  const touchContext = await browser.newContext({
    viewport: { width: 900, height: 900 },
    hasTouch: true,
    isMobile: false,
  });
  const touchPage = await touchContext.newPage();
  await touchPage.addInitScript(seed("light"));
  await touchPage.goto(AFTER + "/", { waitUntil: "domcontentloaded" });
  await settle(touchPage);
  results["coarsePointer"] = await touchPage
    .locator('[data-hive-nav="agents"]')
    .first()
    .evaluate((el) => Math.round(el.getBoundingClientRect().height));
  await touchContext.close();
  } catch (error) {
    log(`checks step failed: ${error.message.split("\n")[0]}`);
    results["checksError"] = error.message.split("\n")[0];
  }

  await browser.close();
  await writeFile(`${OUT}/dom.json`, JSON.stringify(results, null, 2));
  await writeFile(`${OUT}/capture.log`, notes.join("\n") + "\n");
  console.log(JSON.stringify(results, null, 2));
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
