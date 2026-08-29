<script lang="ts">
	import { getContext, onMount } from 'svelte';

	import {
		creditState,
		creditsDismissed,
		dismissCredits,
		fetchCreditBalance,
		formatUsdBalanceFromCredits,
		formatUsdFromCredits,
		type CreditBalance
	} from './credits';

	const i18n: any = getContext('i18n');

	/*
	 * The composer banner (#1063): remaining credits, Claude-style phrasing,
	 * one line above the composer. Self-fetching so the only edit chat's own
	 * Chat.svelte needs is mounting it; a failed fetch renders nothing.
	 */

	let balance: CreditBalance | null = null;
	// Initialized from storage, not left false: dismissal must survive SPA
	// navigation that remounts this component within the same browsing
	// session, or the dismiss button lies.
	let creditsDismissedFlag = creditsDismissed();

	$: state = balance === null ? null : creditState(balance.available_credits);
	$: visible = balance !== null && !creditsDismissedFlag;

	async function load() {
		balance = await fetchCreditBalance();
	}

	function onDismiss() {
		dismissCredits();
		creditsDismissedFlag = true;
	}

	function onFocus() {
		if (document.visibilityState === 'visible') void load();
	}

	onMount(() => {
		void load();
		document.addEventListener('visibilitychange', onFocus);
		return () => document.removeEventListener('visibilitychange', onFocus);
	});
</script>

{#if visible && state}
	<div class="mb-1.5 flex justify-center px-2">
		<div
			class="flex items-center gap-2 rounded-full border px-3 py-1 text-xs {state === 'empty'
				? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300'
				: state === 'low'
					? 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-300'
				: 'border-gray-200/60 bg-gray-50 text-gray-500 dark:border-gray-850 dark:bg-gray-850/40 dark:text-gray-400'}"
			role="status"
		>
			{#if state === 'empty'}
				<span>{$i18n.t("You're out of credits.")}</span>
				{#if balance?.top_up_url}
					<a
						href={balance.top_up_url}
						target="_blank"
						rel="noreferrer"
						class="font-medium underline underline-offset-2"
					>
						{$i18n.t('Top up')}
					</a>
				{/if}
			{:else}
				<span>
					{$i18n.t("You've used {{used}} today · {{remaining}} remaining", {
						used: formatUsdFromCredits(balance?.usage_today_credits ?? 0),
						remaining: formatUsdBalanceFromCredits(balance?.available_credits ?? 0)
					})}
				</span>
			{/if}
			<button
				type="button"
				class="ms-1 rounded-full p-0.5 text-current opacity-50 transition hover:opacity-100"
				aria-label="Dismiss"
				on:click={onDismiss}
			>
				<svg
					xmlns="http://www.w3.org/2000/svg"
					viewBox="0 0 16 16"
					fill="currentColor"
					class="size-3"
				>
					<path
						d="M5.28 4.22a.75.75 0 0 0-1.06 1.06L6.94 8l-2.72 2.72a.75.75 0 1 0 1.06 1.06L8 9.06l2.72 2.72a.75.75 0 1 0 1.06-1.06L9.06 8l2.72-2.72a.75.75 0 0 0-1.06-1.06L8 6.94 5.28 4.22Z"
					/>
				</svg>
			</button>
		</div>
	</div>
{/if}
