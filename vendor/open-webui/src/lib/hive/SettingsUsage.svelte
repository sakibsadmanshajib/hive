<script lang="ts">
	import { getContext, onMount } from 'svelte';

	import {
		creditState,
		formatUsdFromCredits,
		refreshCreditSnapshot,
		type CreditBalance
	} from './credits';

	const i18n: any = getContext('i18n');

	/*
	 * Settings > Usage. The chat frontend's only consumption surface (parity
	 * finding: the settings pane had none at all).
	 *
	 * Lives in lib/hive rather than beside the upstream Settings tabs because
	 * this is Hive authored code and the pre-merge compile guard
	 * (scripts/owui-hive-svelte-compile-check.mjs, run by
	 * scripts/test-owui-hive-frontend.sh) covers this directory only. A Hive
	 * component parked under lib/components is compiled by nothing before
	 * merge, which is the 2026-08-23 AgentSchedules incident that guard was
	 * written to prevent, replayed. SettingsModal imports this the same way
	 * Chat.svelte imports CreditsBanner.
	 *
	 * Deliberately narrower than the Claude Desktop reference this parity-
	 * matches: that surface shows session and weekly quota bars with reset
	 * timers, because its plan is a rate-limited allowance. Hive bills
	 * prepaid credits with no session window and no reset clock (D-046,
	 * D-031), so a reset timer here would be fabricated data on a screen
	 * whose whole job is telling the truth about money. What carries over
	 * from the reference is the shape: a dedicated Usage destination with a
	 * clear number, a manual refresh, and a last-updated stamp.
	 */

	/*
	 * Both figures are TENANT scope, and the labels now say so. The schema has
	 * no per-user allowance at all (see the "Scope is deliberately TENANT
	 * balance" note in apps/control-plane/internal/ledger/chat_balance.go), so
	 * the previous bare "Used today" printed the whole organization's spend
	 * under a personal label. On a money surface that is a correctness bug,
	 * not a copy nit.
	 *
	 * Reuses ./credits, the composer banner's own module, rather than
	 * re-fetching or re-formatting: one source of truth for what the
	 * signed-in user's balance means and how it prints, so this tab and the
	 * banner can never say two different things about the same account.
	 * formatUsdFromCredits is itself a faithful port of
	 * apps/web-console/lib/format/model-pricing.ts and carries the same
	 * honesty invariant: a real balance, zero included, always renders as a
	 * dollar figure, never as the bare integer credit count a prior defect
	 * once put in front of a customer.
	 */

	/*
	 * Starting balance. Null in the app: SettingsModal renders this component
	 * with no balance and the mount fetch below fills it in. Exported so the
	 * rendered surface can be asserted with a known balance and no network,
	 * no database and no DOM, which is what makes transposing the two money
	 * figures a failing test rather than a code review question. See the
	 * server-side render assertions in settings-usage-tab.test.ts.
	 */
	export let balance: CreditBalance | null = null;

	/*
	 * Whether a first load is still in flight. Exported for the same reason as
	 * balance: it is initial state, and the no-data branch (enterprise posture
	 * or a failed first fetch) has to be assertable without waiting on a
	 * network. It only gates the empty case; once a balance exists the figures
	 * render regardless.
	 */
	export let loading = true;

	let lastUpdated: Date | null = null;

	$: state = balance === null ? null : creditState(balance.available_credits);

	async function load() {
		loading = true;
		// A failed refresh keeps the last known good balance and its original
		// stamp; refreshCreditSnapshot owns that policy so it is executable in
		// a test. A transient network blip must not blank a number the
		// customer is reading, and the stamp must never advance on a refresh
		// that learned nothing.
		const next = await refreshCreditSnapshot({ balance, lastUpdated });
		balance = next.balance;
		lastUpdated = next.lastUpdated;
		loading = false;
	}

	onMount(() => {
		if (balance === null) void load();
	});
</script>

<div id="tab-usage" class="flex flex-col h-full justify-between text-sm">
	<div class="overflow-y-scroll max-h-[28rem] md:max-h-full">
		<div class="mb-1 text-sm font-medium">{$i18n.t('Usage')}</div>

		{#if loading && balance === null}
			<div class="text-xs text-gray-500 dark:text-gray-400 py-2">
				{$i18n.t('Loading usage...')}
			</div>
		{:else if balance === null}
			<div class="text-xs text-gray-500 dark:text-gray-400 py-2">
				{$i18n.t("Usage isn't available on this deployment.")}
			</div>
		{:else}
			<div class="flex w-full justify-between py-1">
				<div class="self-center text-xs font-medium">
					{$i18n.t('Organization credit balance')}
				</div>
				<div class="flex items-center gap-2">
					{#if state === 'empty'}
						<span
							class="rounded-full px-2 py-0.5 text-xs font-medium border border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300"
						>
							{$i18n.t('Out of credits')}
						</span>
					{:else if state === 'low'}
						<span
							class="rounded-full px-2 py-0.5 text-xs font-medium border border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-300"
						>
							{$i18n.t('Low')}
						</span>
					{/if}
					<span class="text-sm font-medium" data-testid="usage-available-credits">
						{formatUsdFromCredits(balance.available_credits)}
					</span>
				</div>
			</div>

			<div class="flex w-full justify-between py-1">
				<div class="self-center text-xs font-medium">
					{$i18n.t('Organization usage today')}
				</div>
				<span class="text-sm" data-testid="usage-today-credits">
					{formatUsdFromCredits(balance.usage_today_credits)}
				</span>
			</div>

			{#if balance.top_up_url}
				<div class="flex justify-end pt-2">
					<a
						href={balance.top_up_url}
						target="_blank"
						rel="noreferrer"
						class="px-3 py-1.5 text-xs font-medium bg-gray-100 hover:bg-gray-200 dark:bg-gray-850 dark:hover:bg-gray-800 transition rounded-lg"
					>
						{$i18n.t('Top up')}
					</a>
				</div>
			{/if}
		{/if}
	</div>

	<div class="flex items-center justify-between pt-3 text-xs text-gray-400 dark:text-gray-500">
		<div>
			{#if lastUpdated}
				{$i18n.t('Last updated')}: {lastUpdated.toLocaleTimeString()}
			{/if}
		</div>
		<button
			type="button"
			class="underline hover:text-gray-600 dark:hover:text-gray-300 disabled:opacity-50"
			disabled={loading}
			on:click={() => load()}
		>
			{$i18n.t('Refresh')}
		</button>
	</div>
</div>
