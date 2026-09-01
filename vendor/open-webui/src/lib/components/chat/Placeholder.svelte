<script lang="ts">
	import { toast } from 'svelte-sonner';

	import { onMount, getContext, tick, createEventDispatcher } from 'svelte';
	import { fade } from 'svelte/transition';

	const dispatch = createEventDispatcher();

	import { getChatList } from '$lib/apis/chats';
	import { updateFolderById } from '$lib/apis/folders';

	import {
		user,
		temporaryChatEnabled,
		selectedFolder,
		chats,
		currentChatPage
	} from '$lib/stores';

	import Tooltip from '$lib/components/common/Tooltip.svelte';
	import EyeSlash from '$lib/components/icons/EyeSlash.svelte';
	import MessageInput from './MessageInput.svelte';
	import FolderPlaceholder from './Placeholder/FolderPlaceholder.svelte';
	import FolderTitle from './Placeholder/FolderTitle.svelte';
	import QuickStartChips from '$lib/hive/QuickStartChips.svelte';

	const i18n = getContext('i18n');

	/*
	 * The chat home's headline.
	 *
	 * It used to be the selected model's name, so the first thing on screen in
	 * every demo was a slug like `deepseek-v4-flash`, with that model's own
	 * description under it. Both are gone. Model identity now lives where it is
	 * acted on, which is the chip in the composer's control row, and the
	 * description reaches the model picker as a purpose subtitle. The headline is
	 * the person instead.
	 *
	 * Three literal keys rather than one interpolated one. A missing key falls
	 * back to the key itself (src/lib/i18n/index.ts sets returnEmptyString false),
	 * and a key that is already its own English text degrades to English rather
	 * than to a template with an uninterpolated placeholder left in it.
	 */
	const greetingFor = (hour: number): string =>
		hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';

	const greetingKey = greetingFor(new Date().getHours());

	export let createMessagePair: Function;
	export let stopResponse: Function;

	export let autoScroll = false;

	export let atSelectedModel: Model | undefined;
	export let selectedModels: [''];

	export let history;

	export let prompt = '';
	export let files = [];
	export let messageInput = null;

	export let selectedToolIds = [];
	export let selectedSkillIds = [];
	export let selectedFilterIds = [];
	export let pendingOAuthTools = [];

	export let showCommands = false;

	export let imageGenerationEnabled = false;
	export let codeInterpreterEnabled = false;
	export let webSearchEnabled = false;

	export let onUpload: Function = (e) => {};
	export let onChange = (e) => {};
	export let onWebSearchToggle: Function = () => {};

	export let toolServers = [];

	export let dragged = false;

	// True when viewing a shared folder the current user doesn't own AND lacks write access
	$: folderReadOnly =
		$selectedFolder != null &&
		$selectedFolder.user_id !== $user?.id &&
		$selectedFolder.permission !== 'write';

	// True when the current user does NOT own this folder (hide management menus)
	$: folderNotOwned = $selectedFolder != null && $selectedFolder.user_id !== $user?.id;
</script>

<div class="m-auto w-full max-w-6xl px-2 @2xl:px-20 translate-y-6 py-24 text-center">
	{#if $temporaryChatEnabled}
		<Tooltip
			content={$i18n.t("This chat won't appear in history and your messages will not be saved.")}
			className="w-full flex justify-center mb-0.5"
			placement="top"
		>
			<div class="flex items-center gap-2 text-gray-500 text-base my-2 w-fit">
				<EyeSlash strokeWidth="2.5" className="size-4" />{$i18n.t('Temporary Chat')}
			</div>
		</Tooltip>
	{/if}

	<div
		class="w-full text-3xl text-gray-800 dark:text-gray-100 text-center flex items-center gap-4 font-primary"
	>
		<div class="w-full flex flex-col justify-center items-center">
			{#if $selectedFolder}
				<FolderTitle
					folder={$selectedFolder}
					readOnly={folderNotOwned}
					onUpdate={async (folder) => {
						currentChatPage.set(1);
						await chats.set(await getChatList(localStorage.token, 1));
					}}
					onDelete={async () => {
						currentChatPage.set(1);
						await chats.set(await getChatList(localStorage.token, 1));

						selectedFolder.set(null);
					}}
				/>
			{:else}
				<div class="hv-greeting text-gray-850 dark:text-gray-100 px-5 max-w-3xl">
					{$i18n.t(greetingKey)}{$user?.name ? `, ${$user.name}` : ''}
				</div>
			{/if}

			<div class="text-base font-normal @md:max-w-3xl w-full py-3 {atSelectedModel ? 'mt-2' : ''}">
				{#if !($selectedFolder && folderReadOnly)}
					<MessageInput
						bind:this={messageInput}
						{history}
						bind:selectedModels
						bind:files
						bind:prompt
						bind:autoScroll
						bind:selectedToolIds
						bind:selectedSkillIds
						bind:selectedFilterIds
						bind:imageGenerationEnabled
						bind:codeInterpreterEnabled
						bind:webSearchEnabled
						bind:atSelectedModel
						bind:showCommands
						bind:dragged
						{pendingOAuthTools}
						{toolServers}
						{stopResponse}
						{createMessagePair}
						placeholder={$i18n.t('How can I help you today?')}
						{onChange}
						{onUpload}
						{onWebSearchToggle}
						on:submit={(e) => {
							dispatch('submit', e.detail);
						}}
					/>
				{/if}
			</div>

			{#if !$selectedFolder}
				<QuickStartChips
					on:pick={(e) => {
						// setText, not `prompt = ...`: the composer is a rich-text
						// editor whose document is the source of truth, so assigning
						// the bound string leaves the visible field empty. setText
						// writes the editor and focuses it in one call.
						messageInput?.setText(e.detail);
					}}
				/>
			{/if}
		</div>
	</div>

	{#if $selectedFolder}
		<div
			class="mx-auto px-4 md:max-w-3xl md:px-6 font-primary min-h-62"
			in:fade={{ duration: 200, delay: 200 }}
		>
			<FolderPlaceholder folder={$selectedFolder} />
		</div>
	{/if}
</div>
