<script lang="ts">
	import { getContext, onMount } from 'svelte';

	import {
		creditState,
		fetchCreditBalance,
		formatUsdFromCredits,
		type CreditBalance
	} from '$lib/hive/credits';

	const i18n: any = getContext('i18n');

	/*
	 * Settings > Usage. The chat frontend's only per-account consumption
	 * surface (parity finding: the settings pane had none at all).
	 *
	 * Deliberately narrower than the Claude Desktop reference this parity-
	 * matches: that surface shows session/weekly quota bars with reset
	 * timers, because its plan is a rate-limited allowance. Hive bills
	 * prepaid credits with no session window and no reset clock (D-046,
	 * D-031), so a reset timer here would be fabricated data on a screen
	 * whose whole job is telling the truth about money. What carries over
	 * from the reference is the shape: a dedicated Usage destination with a
	 * clear number, a manual refresh, and a "last updated" stamp.
	 *
	 * Reuses `$lib/hive/credits` (the composer banner's own module) rather
	 * than re-fetching or re-formatting: one source of truth for what the
	 * signed-in user's balance means and how it prints, so this tab and the
	 * banner can never say two different things about the same account.
	 * `formatUsdFromCredits` is itself a faithful port of
	 * `apps/web-console/lib/format/model-pricing.ts`'s `formatUsdFromCredits`
	 * (see that module's own header comment for why the two cannot share
	 * code across the Next.js console and this SvelteKit build) and carries
	 * the same honesty invariant: a real balance, zero included, always
	 * renders as a dollar figure, and never as the bare integer credit
	 * count a prior defect once put in front of a customer ("9,789,478,244
	 * remaining").
	 */

	let balance: CreditBalance | null = null;
	let loading = true;
	let lastUpdated: Date | null = null;

	$: state = balance === null ? null : creditState(balance.available_credits);

	async function load() {
		loading = true;
		balance = await fetchCreditBalance();
		loading = false;
		lastUpdated = new Date();
	}

	onMount(() => {
		void load();
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
				<div class="self-center text-xs font-medium">{$i18n.t('Credit balance')}</div>
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
				<div class="self-center text-xs font-medium">{$i18n.t('Used today')}</div>
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
