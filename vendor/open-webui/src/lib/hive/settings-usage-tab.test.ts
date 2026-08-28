import { describe, expect, it } from 'vitest';
import { existsSync } from 'node:fs';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// Regression guard for the P0.5 settings retitle + Usage tab wave
// (parity finding: the right pane was literally titled "WebUI Settings"
// and there was no consumption/credit surface anywhere in Settings). This
// repo has no component-test harness for the chat frontend, so these tests
// pin the rendered surface at source level, matching the sibling
// settings-declutter.test.ts in this directory.

const component = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

const settingsModal = () => component('../components/chat/SettingsModal.svelte');
const general = () => component('../components/chat/Settings/General.svelte');
const usageTab = () => component('../components/chat/Settings/Usage.svelte');

describe('General tab no longer reads as stock Open WebUI branding', () => {
	it('drops the literal "WebUI Settings" section header', () => {
		expect(general()).not.toContain("t('WebUI Settings')");
	});

	it('renames it to a Hive-authored label', () => {
		expect(general()).toContain("t('Chat Preferences')");
	});
});

describe('Usage tab wiring', () => {
	it('exists as its own component', () => {
		expect(
			existsSync(
				fileURLToPath(
					new URL('../components/chat/Settings/Usage.svelte', import.meta.url)
				)
			)
		).toBe(true);
	});

	it('is imported and rendered by the settings modal', () => {
		const src = settingsModal();
		expect(src).toContain("from './Settings/Usage.svelte'");
		expect(src).toContain('<Usage');
	});

	it('is a real, always-available tab entry (not gated dead like the orphaned tools tab)', () => {
		const src = settingsModal();
		expect(src).toContain("id: 'usage'");
		expect(src).toContain("aria-controls=\"tab-usage\"");
	});

	it('carries billing-relevant search keywords so the settings search box can find it', () => {
		const src = settingsModal();
		const usageBlockStart = src.indexOf("id: 'usage'");
		expect(usageBlockStart).toBeGreaterThan(-1);
		const usageBlock = src.slice(usageBlockStart, usageBlockStart + 600);
		for (const keyword of ["'credits'", "'balance'", "'billing'"]) {
			expect(usageBlock).toContain(keyword);
		}
	});
});

describe('Usage tab renders credits through the shared honesty-invariant formatter', () => {
	it('imports the ported formatter rather than re-deriving one', () => {
		const src = usageTab();
		expect(src).toContain("from '$lib/hive/credits'");
		expect(src).toContain('formatUsdFromCredits');
	});

	it('never interpolates the raw available/usage-today credit integers directly', () => {
		// The regression this guards: "9,789,478,244 remaining" reaching a
		// customer verbatim. Every place the balance fields are read for
		// display must route through formatUsdFromCredits(...), never a bare
		// {balance.available_credits} / {balance.usage_today_credits}.
		const src = usageTab();
		expect(src).not.toContain('{balance.available_credits}');
		expect(src).not.toContain('{balance.usage_today_credits}');
		expect(src).not.toContain('{balance?.available_credits}');
		expect(src).not.toContain('{balance?.usage_today_credits}');
		// Both fields still appear, but only ever as formatter arguments.
		expect(src).toContain('formatUsdFromCredits(balance.available_credits)');
		expect(src).toContain('formatUsdFromCredits(balance.usage_today_credits)');
	});

	it('never fabricates a session/weekly reset timer the prepaid credit model has no data for', () => {
		// The Claude Desktop reference this parity-matches shows "Resets in 2
		// hr 22 min" style countdowns, backed by a real rate-limited plan
		// quota. Hive bills prepaid credits with no such window (D-046,
		// D-031); inventing a countdown here would be fabricated data on a
		// screen whose whole job is telling the truth about money.
		const src = usageTab();
		expect(src.toLowerCase()).not.toContain('resets in');
		expect(src.toLowerCase()).not.toContain('resets thu');
	});

	it('handles the no-data (enterprise posture / fetch failure) case explicitly rather than rendering blank', () => {
		const src = usageTab();
		expect(src).toContain("balance === null");
	});
});
