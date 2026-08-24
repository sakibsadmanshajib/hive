// Visual proof for the Scheduled nav row and schedules empty state (PR #1118).
//
// One throwaway Open WebUI container built from this branch's diff
// (hive-open-webui:scheduled-proof), auth disabled so no fixture account is
// touched, captured with Playwright inside the pinned Playwright image.
//
// Run inside mcr.microsoft.com/playwright:v1.51.1-noble with the Open WebUI
// container already up on the same docker network.

import { chromium } from "playwright";
import { writeFile } from "node:fs/promises";

const OUT = process.env.OUT_DIR ?? "/out";
const BASE = process.env.BASE_URL ?? "http://schedproof-owui:8080";

const VIEWPORT = { width: 1440, height: 900 };

const lines = [];
const log = (line) => {
	lines.push(line);
	console.log(line);
};

const browser = await chromium.launch();
try {
	const context = await browser.newContext({ viewport: VIEWPORT });
	// Theme and sidebar expansion are localStorage reads before boot; seed both
	// so the capture shows the default expanded sidebar in light theme.
	await context.addInitScript(`
		try {
			localStorage.setItem('theme', 'light');
			localStorage.setItem('sidebar', 'true');
		} catch (e) {}
	`);
	const page = await context.newPage();
	const consoleErrors = [];
	page.on("console", (m) => {
		if (m.type() === "error") consoleErrors.push(m.text());
	});

	log(`target ${BASE}`);
	await page.goto(`${BASE}/`, { waitUntil: "domcontentloaded" });

	// 1. Sidebar row: the Scheduled entry renders in the shell nav.
	const row = page.locator('[data-hive-nav="scheduled"]');
	await row.waitFor({ state: "visible", timeout: 30000 });
	const href = await row.getAttribute("href");
	const label = (await row.innerText()).trim();
	log(`nav row label="${label}" href=${href}`);
	if (href !== "/schedules") {
		throw new Error(`expected /schedules, got ${href}`);
	}

	await page.screenshot({ path: `${OUT}/01-sidebar-scheduled-row.png`, fullPage: false });
	log("captured 01-sidebar-scheduled-row.png");

	// 2. The destination: top-level /schedules route, Claude reference style
	//    empty state (title, explainer, New button).
	await page.goto(`${BASE}/schedules`, { waitUntil: "domcontentloaded" });
	await page.getByText("Templated runs kicked off on schedule.").waitFor({ state: "visible", timeout: 30000 });
	const heading = (await page.locator(".hv-panel h2").innerText()).trim();
	const newButtons = await page.getByRole("button", { name: "New", exact: true }).count();
	log(`page title heading="${heading}" new_buttons=${newButtons} url=${page.url()}`);

	await page.screenshot({ path: `${OUT}/02-schedules-empty-state.png`, fullPage: false });
	log("captured 02-schedules-empty-state.png");

	log(`console_errors=${consoleErrors.length}${consoleErrors.length ? ` :: ${JSON.stringify(consoleErrors)}` : ""}`);
	log(`browser_title=${await page.title()}`);
} finally {
	await writeFile(`${OUT}/capture.log`, lines.join("\n") + "\n");
	await browser.close();
}
