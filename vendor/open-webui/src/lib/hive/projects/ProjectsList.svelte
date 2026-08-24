<script lang="ts">
	/*
	 * The Projects index: the D-045 destination holding context brought in.
	 *
	 * Hive authored. Backed by src/lib/hive/projects/projects.ts, which binds a
	 * project to a knowledge collection on the pinned backend image; see that
	 * file for the storage seam. This component is presentation and intent
	 * only: list, search, sort by last updated, create.
	 */
	import { getContext, onMount } from 'svelte';
	import { goto } from '$app/navigation';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import { WEBUI_NAME } from '$lib/stores';

	import {
		type HiveProject,
		ProjectError,
		createProject,
		listProjects
	} from './projects';

	const i18n: any = getContext('i18n');

	let projects: HiveProject[] = [];
	let loading = true;
	let error = '';
	let query = '';
	let showCreate = false;
	let newName = '';
	let newDescription = '';
	let creating = false;

	onMount(async () => {
		try {
			projects = (await listProjects(localStorage.getItem('token') ?? '')).sort(
				(a, b) => b.updatedAt - a.updatedAt
			);
		} catch (err) {
			error =
				err instanceof ProjectError
					? err.message
					: $i18n.t('Projects could not be loaded.');
		} finally {
			loading = false;
		}
	});

	const filtered = () => {
		const q = query.trim().toLowerCase();
		const sorted = [...projects].sort((a, b) => b.updatedAt - a.updatedAt);
		if (!q) return sorted;
		return sorted.filter(
			(p) => p.name.toLowerCase().includes(q) || p.description.toLowerCase().includes(q)
		);
	};

	const relativeDate = (epochSeconds: number): string => {
		if (!epochSeconds) return '';
		const diffMs = Date.now() - epochSeconds * 1000;
		const minutes = Math.floor(diffMs / 60000);
		if (minutes < 1) return $i18n.t('just now');
		if (minutes < 60) return $i18n.t('{{count}}m ago', { count: minutes });
		const hours = Math.floor(minutes / 60);
		if (hours < 24) return $i18n.t('{{count}}h ago', { count: hours });
		const days = Math.floor(hours / 24);
		if (days < 30) return $i18n.t('{{count}}d ago', { count: days });
		return new Date(epochSeconds * 1000).toLocaleDateString();
	};

	const openCreate = () => {
		newName = '';
		newDescription = '';
		showCreate = true;
	};

	const submitCreate = async () => {
		if (!newName.trim() || creating) return;
		creating = true;
		error = '';
		try {
			const project = await createProject(
				localStorage.getItem('token') ?? '',
				{ name: newName.trim(), description: newDescription.trim() }
			);
			await goto(`/projects/${project.id}`);
		} catch (err) {
			error =
				err instanceof ProjectError ? err.message : $i18n.t('The project could not be created.');
		} finally {
			creating = false;
		}
	};
</script>

<svelte:head>
	<title>{$i18n.t('Projects')} • {$WEBUI_NAME}</title>
</svelte:head>

<div class="w-full flex flex-col px-4 md:px-10 py-6 gap-6 max-w-5xl mx-auto">
	<div class="flex items-center justify-between gap-3">
		<h1 class="text-2xl font-semibold text-gray-850 dark:text-gray-100">{$i18n.t('Projects')}</h1>
		<Tooltip content={$i18n.t('Create a project')} placement="bottom">
			<button
				id="projects-new-button"
				class="px-3.5 py-2 text-sm font-medium rounded-xl bg-black text-white dark:bg-white dark:text-black hover:opacity-80 transition"
				on:click={openCreate}
			>
				{$i18n.t('New project')}
			</button>
		</Tooltip>
	</div>

	{#if error}
		<p role="alert" class="text-sm text-red-500">{error}</p>
	{/if}

	<input
		id="projects-search"
		class="w-full px-4 py-2.5 text-sm rounded-xl bg-gray-50 dark:bg-gray-900 border border-gray-200 dark:border-gray-800 outline-none focus:border-gray-400 dark:focus:border-gray-600 transition"
		type="search"
		placeholder={$i18n.t('Search projects')}
		bind:value={query}
	/>

	{#if loading}
		<p class="text-sm text-gray-500" role="status">{$i18n.t('Loading...')}</p>
	{:else if filtered().length === 0}
		<div class="text-center py-16">
			<p class="text-sm text-gray-500">{$i18n.t('No projects yet')}</p>
			<p class="text-xs text-gray-400 mt-1">
				{$i18n.t('Projects hold the documents and conversations you work against.')}
			</p>
		</div>
	{:else}
		<ul class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3" aria-label={$i18n.t('Projects')}>
			{#each filtered() as project (project.id)}
				<li>
					<a
						href={`/projects/${project.id}`}
						class="flex h-full flex-col p-4 rounded-2xl border border-gray-200 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-900 transition"
					>
						<div class="text-sm font-medium text-gray-850 dark:text-gray-100 truncate">
							{project.name}
						</div>
						<!-- Subtitle row per the Claude reference: description first, then recency -->
						<div class="mt-1 text-xs text-gray-500 line-clamp-2 min-h-[2rem]">
							{project.description || $i18n.t('No description')}
						</div>
						<div class="mt-auto pt-2 text-[11px] text-gray-400">{relativeDate(project.updatedAt)}</div>
					</a>
				</li>
			{/each}
		</ul>
	{/if}
</div>

{#if showCreate}
	<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40" role="dialog" aria-modal="true">
		<button class="absolute inset-0 cursor-default" aria-label={$i18n.t('Close')} on:click={() => (showCreate = false)} />
		<form
			class="relative w-full max-w-md mx-4 p-5 rounded-2xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 shadow-xl flex flex-col gap-3"
			on:submit|preventDefault={submitCreate}
		>
			<h2 class="text-base font-semibold text-gray-850 dark:text-gray-100">{$i18n.t('New project')}</h2>
			{#if error}
				<p role="alert" class="text-sm text-red-500">{error}</p>
			{/if}
			<label class="flex flex-col gap-1">
				<span class="text-xs font-medium text-gray-500">{$i18n.t('Name')}</span>
				<input
					id="projects-create-name"
					class="px-3 py-2 text-sm rounded-lg bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 outline-none focus:border-gray-400 transition"
					bind:value={newName}
					maxlength={255}
					required
				/>
			</label>
			<label class="flex flex-col gap-1">
				<span class="text-xs font-medium text-gray-500">{$i18n.t('Description')}</span>
				<textarea
					id="projects-create-description"
					class="px-3 py-2 text-sm rounded-lg bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 outline-none focus:border-gray-400 transition resize-y"
					bind:value={newDescription}
					rows="3"
					maxlength={1000}
				/>
			</label>
			<div class="flex justify-end gap-2 mt-1">
				<button
					type="button"
					class="px-3 py-2 text-sm rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800 transition"
					on:click={() => (showCreate = false)}
				>
					{$i18n.t('Cancel')}
				</button>
				<button
					type="submit"
					disabled={!newName.trim() || creating}
					class="px-3 py-2 text-sm font-medium rounded-lg bg-black text-white dark:bg-white dark:text-black disabled:opacity-40 hover:opacity-80 transition"
				>
					{$i18n.t('Create')}
				</button>
			</div>
		</form>
	</div>
{/if}
