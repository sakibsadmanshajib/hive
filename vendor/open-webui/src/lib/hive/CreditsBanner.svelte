<script lang="ts">
	import { onMount } from 'svelte';

	import {
		creditState,
		creditsDismissed,
		dismissCredits,
		fetchCreditBalance,
		formatCredits,
		type CreditBalance
	} from './credits';

	/*
	 * The composer banner (#1063): remaining credits, Claude-style phrasing,
	 * one line above the composer. Self-fetching so the only edit chat's own
	 * Chat.svelte needs is mounting it; a failed fetch renders nothing.
	 */

	let balance: CreditBalance | null = null;
	let creditsDismissedFlag = false;

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
				<span>You're out of credits.</span>
				<a
					href="https://console-hive.scubed.co/console/billing"
					target="_blank"
					rel="noreferrer"
					class="font-medium underline underline-offset-2"
				>
					Top up
				</a>
			{:else}
				<span>
					You've used {formatCredits(balance?.usage_today_credits ?? 0)} credits today
					&middot; {formatCredits(balance?.available_credits ?? 0)} remaining
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
