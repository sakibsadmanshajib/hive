<script>
	import { getContext } from 'svelte';
	const i18n = getContext('i18n');

	import StatusItem from './StatusHistory/StatusItem.svelte';
	import equal from 'fast-deep-equal';
	export let statusHistory = [];
	export let expand = false;

	let showHistory = true;

	$: if (expand) {
		showHistory = true;
	} else {
		showHistory = false;
	}

	let history = [];
	let status = null;

	$: if (history && history.length > 0) {
		status = history.at(-1);
	}

	$: if (!equal(statusHistory, history)) {
		history = statusHistory;
	}
</script>

{#if history && history.length > 0}
	{#if status?.hidden !== true}
		<div class="text-sm flex flex-col w-full">
			<!--
				The chain of steps already taken, ABOVE the newest one, and
				without repeating it (Hive: issues #1622, #1504).

				Upstream rendered the newest entry in the toggle button and then
				rendered the whole list underneath it, so expanding showed the
				newest line twice and showed it above the older steps it came
				after. That was survivable for a chat turn, where the list is one
				or two statuses nobody expands. It is not survivable for a Cowork
				run, whose step chain is the entire visible output until the run
				finishes: the run turn opens expanded (ResponseMessage passes
				`expand` for a turn carrying a run id), so this is the ordinary
				render for it rather than a rare one.

				Oldest first, newest last, which is the order the steps happened
				in and the order the button below then continues. The button
				stays: it is the toggle, and a long run needs to be collapsible.
			-->
			{#if showHistory && history.length > 1}
				<div class="flex flex-row">
					<div class="w-full">
						{#each history.slice(0, -1) as status}
							<div class="flex items-stretch gap-2 mb-1">
								<div class=" ">
									<div class="pt-3 px-1 mb-1.5">
										<span class="relative flex size-1.5 rounded-full justify-center items-center">
											<span
												class="relative inline-flex size-1.5 rounded-full bg-gray-500 dark:bg-gray-400"
											></span>
										</span>
									</div>
									<div
										class="w-[0.5px] ml-[6.5px] h-[calc(100%-14px)] bg-gray-300 dark:bg-gray-700"
									/>
								</div>

								<StatusItem {status} done={true} />
							</div>
						{/each}
					</div>
				</div>
			{/if}

			<button
				class="w-full"
				aria-label={$i18n.t('Toggle status history')}
				aria-expanded={showHistory}
				on:click={() => {
					showHistory = !showHistory;
				}}
			>
				<div class="flex items-start gap-2">
					<StatusItem {status} />
				</div>
			</button>
		</div>
	{/if}
{/if}
