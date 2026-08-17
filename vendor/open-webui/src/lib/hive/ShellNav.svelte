<script lang="ts">
	import { getContext } from 'svelte';
	import { page } from '$app/stores';

	import Tooltip from '$lib/components/common/Tooltip.svelte';

	import ShellNavIcon from './ShellNavIcon.svelte';
	import { HIVE_NAV, isNavItemActive } from './nav';

	const i18n: any = getContext('i18n');

	/**
	 * Rail mode is the 56px collapsed sidebar: icons only, label carried by a
	 * tooltip and by the accessible name, never by the tooltip alone.
	 */
	export let rail = false;

	export let onNavigate: () => void = () => {};

	$: pathname = $page.url?.pathname ?? '/';
</script>

<nav class={rail ? 'hv-nav hv-nav-rail' : 'hv-nav'} aria-label={$i18n.t('Hive')}>
	{#each HIVE_NAV as item (item.id)}
		{@const active = isNavItemActive(item, pathname)}
		{#if rail}
			<Tooltip content={$i18n.t(item.label)} placement="right">
				<a
					id="hive-rail-{item.id}"
					data-hive-nav={item.id}
					class="hv-nav-row hv-nav-row-rail"
					class:hv-nav-row-active={active}
					href={item.href}
					draggable="false"
					aria-current={active ? 'page' : undefined}
					aria-label={$i18n.t(item.label)}
					on:click={onNavigate}
				>
					<ShellNavIcon name={item.icon} />
				</a>
			</Tooltip>
		{:else}
			<a
				id="hive-nav-{item.id}"
				data-hive-nav={item.id}
				class="hv-nav-row"
				class:hv-nav-row-active={active}
				href={item.href}
				draggable="false"
				aria-current={active ? 'page' : undefined}
				on:click={onNavigate}
			>
				<ShellNavIcon name={item.icon} />
				<span class="hv-nav-label">{$i18n.t(item.label)}</span>
			</a>
		{/if}
	{/each}
</nav>
