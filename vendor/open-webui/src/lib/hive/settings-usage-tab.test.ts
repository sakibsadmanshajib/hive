import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { render } from 'svelte/server';
import { readable } from 'svelte/store';

import SettingsUsage from './SettingsUsage.svelte';
import {
	formatUsdBalanceFromCredits,
	formatUsdFromCredits,
	type CreditBalance,
	type CreditSnapshot
} from './credits';

/*
 * Regression guard for the P0.5 settings retitle plus Usage tab wave (parity
 * finding: the right pane was literally titled "WebUI Settings" and there was
 * no consumption or credit surface anywhere in Settings).
 *
 * Two kinds of assertion here, and the difference matters. The Usage component
 * is rendered for real, server side, so a transposition of its two money
 * figures fails a test instead of shipping. SettingsModal is pinned at source
 * level only: it imports roughly forty sibling components and stores, none of
 * which exist in the scratch tree the pre-merge gate runs in, so rendering it
 * is not reachable before merge. The source pins below are therefore written
 * against the exact mutations they have to catch, an emptied click handler
 * included, rather than against the mere presence of a tab id.
 */

const readSource = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

const settingsModal = () => readSource('../components/chat/SettingsModal.svelte');
const general = () => readSource('../components/chat/Settings/General.svelte');
const locale = (code: string) =>
	JSON.parse(readSource('../i18n/locales/' + code + '/translation.json'));

/*
 * A rendered Usage tab, with the i18n store the app supplies through context.
 * Server side render on purpose: it needs no DOM, so it runs identically in
 * the scratch-tree gate (scripts/test-owui-hive-frontend.sh) and in the image
 * build's in-place run, and the vendored lockfile does not have to grow a
 * jsdom for it. The translator is the identity function, so every assertion
 * below reads the English source string.
 */
const renderUsage = (initial: CreditSnapshot | null): string => {
	const t = (key: string): string => key;
	const context = new Map();
	context.set('i18n', readable({ t }));
	return render(SettingsUsage, { props: { initial }, context }).body;
};

// Two deliberately different magnitudes. Equal figures would render the same
// string in both slots and a transposition would be invisible, which is the
// exact defect these assertions exist to catch. The balance is deliberately
// not a whole number of cents either: it renders $12.49 through the balance
// formatter and $12.50 through the price formatter, so the row assertions
// below pin which of the two the component actually calls rather than passing
// either way (issue 1345).
const AVAILABLE_CREDITS = 12_496_364_207;
const USAGE_TODAY_CREDITS = 340_000_000;

const snapshotFixture = (overrides: Partial<CreditBalance> = {}): CreditSnapshot => ({
	balance: {
		available_credits: AVAILABLE_CREDITS,
		usage_today_credits: USAGE_TODAY_CREDITS,
		...overrides
	},
	lastUpdated: null
});

// A snapshot whose first load came back with nothing: the enterprise posture
// and the failed-first-fetch case are the same state here.
const EMPTY_SNAPSHOT: CreditSnapshot = { balance: null, lastUpdated: null };

/*
 * The text a given slot actually renders. Reading the value out of the span
 * that carries the test id, rather than searching the whole document, is what
 * makes "the right number in the wrong row" a failure: a document-wide
 * toContain would pass just as happily with the two figures swapped.
 */
const valueForTestId = (html: string, testId: string): string => {
	const marker = 'data-testid="' + testId + '"';
	const at = html.indexOf(marker);
	if (at === -1) return '';
	const open = html.indexOf('>', at);
	const close = html.indexOf('</span>', open);
	// Sliced between the span's own boundaries and compared whole, rather
	// than stripped of markup: the value is a formatted currency string with
	// no nested elements, so any tag or comment turning up inside it is a
	// real change in what the slot renders and should fail this comparison
	// rather than be quietly removed from it.
	return html.slice(open + 1, close).trim();
};

describe('General tab no longer reads as stock Open WebUI branding', () => {
	it('drops the literal WebUI Settings section header', () => {
		expect(general()).not.toContain("t('WebUI Settings')");
	});

	it('renames it to a Hive-authored label', () => {
		expect(general()).toContain("t('Chat Preferences')");
	});

	it('carries the retitle into the search keywords, so the old title is not the only way to find the tab', () => {
		const src = settingsModal();
		// Both boundaries asserted before slicing. A missing end marker makes
		// indexOf return -1, and slice(start, -1) would quietly widen this to
		// most of the file, so the keywords could satisfy it from any other
		// tab's descriptor.
		const start = src.indexOf("id: 'general'");
		const end = src.indexOf("id: 'account'");
		expect(start).toBeGreaterThan(-1);
		expect(end).toBeGreaterThan(start);
		const generalBlock = src.slice(start, end);
		expect(generalBlock).toContain("'chat preferences'");
		expect(generalBlock).toContain("'chatpreferences'");
	});
});

describe('the new strings exist in the locale files, not only in the markup', () => {
	// The rename drops a string that WAS translated in bn-BD: the old key had
	// a Bengali value. Bangladesh is the first market, so the replacement and
	// every new money label carry a Bengali translation rather than silently
	// falling back to English there. The other 61 locales fall back until
	// their own translators reach them, which is how every other untranslated
	// key in this fork already behaves.
	const strings = [
		'Chat Preferences',
		'Organization credit balance',
		'Organization usage today',
		'Out of credits',
		'Top up',
		'Last updated',
		'Loading usage...',
		"Usage isn't available on this deployment."
	];

	it('registers every new string in en-US, the key catalogue', () => {
		const enUS = locale('en-US');
		for (const key of strings) {
			expect(Object.prototype.hasOwnProperty.call(enUS, key)).toBe(true);
		}
	});

	it('translates every rendered string in bn-BD, the first market', () => {
		const bnBD = locale('bn-BD');
		const rendered = strings.concat(['Usage', 'Refresh', 'Low']);
		for (const key of rendered) {
			expect(bnBD[key] ?? '').not.toBe('');
		}
	});
});

describe('Usage tab wiring in the settings modal', () => {
	it('imports the component from lib/hive, where the compile guard covers it', () => {
		// Not a style preference. scripts/test-owui-hive-frontend.sh compiles
		// lib/hive and nothing else, so a Hive component parked under
		// lib/components is compiled by no pre-merge job at all.
		const src = settingsModal();
		expect(src).toContain("from '$lib/hive/SettingsUsage.svelte'");
		expect(src).toContain('<Usage');
	});

	it('registers the tab and points its button at the panel', () => {
		const src = settingsModal();
		expect(src).toContain("id: 'usage'");
		expect(src).toContain('aria-controls="tab-usage"');
	});

	it('carries billing-relevant search keywords so the settings search box can find it', () => {
		const src = settingsModal();
		const start = src.indexOf("id: 'usage'");
		expect(start).toBeGreaterThan(-1);
		const block = src.slice(start, start + 600);
		for (const keyword of ["'credits'", "'balance'", "'billing'"]) {
			expect(block).toContain(keyword);
		}
	});

	it('wires the rail button to actually select the tab, so an emptied handler fails here', () => {
		// The dead-tab regression this is named for: a button that renders,
		// highlights and reads correctly to a screen reader while its click
		// handler does nothing. Pinned inside the usage button block only, so
		// a selectedTab assignment somewhere else in this 900 line file cannot
		// satisfy it.
		const src = settingsModal();
		const start = src.indexOf('aria-controls="tab-usage"');
		expect(start).toBeGreaterThan(-1);
		const end = src.indexOf('</button>', start);
		expect(end).toBeGreaterThan(start);
		const button = src.slice(start, end);
		expect(button).toContain('on:click');
		expect(button).toContain("selectedTab = 'usage'");
	});

	it('renders the panel for the selected tab', () => {
		const src = settingsModal();
		expect(src).toContain("selectedTab === 'usage'");
	});

	it('hides the tab where the deployment has no credits surface at all', () => {
		// Enterprise deployments never wire the chat container credits proxy
		// and it fails closed with a 404; silent absence is that posture
		// documented behavior in deploy/docker/owui-patches/hive_credits.py. A
		// tab permanently stuck on the not-available sentence would invert it,
		// so availability is probed and the rail entry is gated on it.
		const src = settingsModal();
		const start = src.indexOf('const getAvailableSettings');
		expect(start).toBeGreaterThan(-1);
		const block = src.slice(start, start + 900);
		expect(block).toContain("tab.id === 'usage'");
		expect(block).toContain('return creditsAvailable;');
	});
});

describe('Usage tab, rendered', () => {
	it('puts each money figure in its own labelled row', () => {
		// The mutation this exists to catch: swap the two figures so the
		// balance row shows today spend and vice versa. Every source-level
		// assertion in this file stays green through that swap; this one does
		// not, because it reads the rendered order.
		const html = renderUsage(snapshotFixture());
		const balanceLabel = html.indexOf('Organization credit balance');
		const todayLabel = html.indexOf('Organization usage today');
		expect(balanceLabel).toBeGreaterThan(-1);
		expect(todayLabel).toBeGreaterThan(balanceLabel);

		const balanceRow = html.slice(balanceLabel, todayLabel);
		const todayRow = html.slice(todayLabel);

		expect(balanceRow).toContain(formatUsdBalanceFromCredits(AVAILABLE_CREDITS));
		expect(balanceRow).not.toContain(formatUsdFromCredits(USAGE_TODAY_CREDITS));
		expect(todayRow).toContain(formatUsdFromCredits(USAGE_TODAY_CREDITS));
		expect(todayRow).not.toContain(formatUsdBalanceFromCredits(AVAILABLE_CREDITS));
	});

	it('keeps each figure in the slot its own test id names', () => {
		const html = renderUsage(snapshotFixture());
		expect(valueForTestId(html, 'usage-available-credits')).toBe(
			formatUsdBalanceFromCredits(AVAILABLE_CREDITS)
		);
		expect(valueForTestId(html, 'usage-today-credits')).toBe(
			formatUsdFromCredits(USAGE_TODAY_CREDITS)
		);
	});

	it('never renders a bare credit integer, the defect a customer once read as 9,789,478,244', () => {
		const html = renderUsage(snapshotFixture());
		expect(html).not.toContain(String(AVAILABLE_CREDITS));
		expect(html).not.toContain(String(USAGE_TODAY_CREDITS));
	});

	it('flags an empty balance rather than printing a bare zero', () => {
		// Two decimals, which is what the balance formatter renders for an
		// exact zero and what the console prints for the same account. The
		// point of the assertion is unchanged: an empty balance is a real,
		// readable dollar figure beside the badge, never a bare 0 and never a
		// blank.
		const html = renderUsage(snapshotFixture({ available_credits: 0 }));
		expect(html).toContain('Out of credits');
		expect(valueForTestId(html, 'usage-available-credits')).toBe('$0.00');
	});

	it('flags a low balance below the shared threshold', () => {
		const html = renderUsage(snapshotFixture({ available_credits: 100_000_000 }));
		expect(html).toContain('Low');
	});

	it('offers the top-up link only where the deployment names one', () => {
		const without = renderUsage(snapshotFixture());
		expect(without).not.toContain('Top up');
		const url = 'https://example.invalid/billing';
		const withLink = renderUsage(snapshotFixture({ top_up_url: url }));
		expect(withLink).toContain('Top up');
		expect(withLink).toContain(url);
	});

	it('says so explicitly when there is no balance to show, rather than rendering blank', () => {
		const html = renderUsage(EMPTY_SNAPSHOT);
		expect(html).toContain('available on this deployment');
	});

	it('never fabricates a reset timer the prepaid credit model has no data for', () => {
		// The Claude Desktop reference shows countdowns backed by a
		// rate-limited plan quota. Hive bills prepaid credits with no such
		// window, per D-046 and D-031.
		const html = renderUsage(snapshotFixture()).toLowerCase();
		expect(html).not.toContain('resets in');
		expect(html).not.toContain('resets on');
	});
});
