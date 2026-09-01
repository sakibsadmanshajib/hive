<script lang="ts">
	import { models, showSettings, settings, mobile, config } from '$lib/stores';
	import { onMount, tick, getContext } from 'svelte';
	import { toast } from 'svelte-sonner';
	import Selector from './ModelSelector/Selector.svelte';

	import { updateUserSettings } from '$lib/apis/users';
	import equal from 'fast-deep-equal';
	const i18n = getContext('i18n');

	export let selectedModels = [''];
	export let disabled = false;

	export let showSetDefault = true;

	const saveDefaultModel = async () => {
		const hasEmptyModel = selectedModels.filter((it) => it === '');
		if (hasEmptyModel.length) {
			toast.error($i18n.t('Choose a model before saving...'));
			return;
		}
		settings.set({ ...$settings, models: selectedModels });
		await updateUserSettings(localStorage.token, { ui: $settings });

		toast.success($i18n.t('Default model updated'));
	};

	const pinModelHandler = async (modelId) => {
		let pinnedModels = $settings?.pinnedModels ?? [];

		if (pinnedModels.includes(modelId)) {
			pinnedModels = pinnedModels.filter((id) => id !== modelId);
		} else {
			pinnedModels = [...new Set([...pinnedModels, modelId])];
		}

		settings.set({ ...$settings, pinnedModels: pinnedModels });
		await updateUserSettings(localStorage.token, { ui: $settings });
	};

	// hive (issue #1626): one conversation, one model.
	//
	// Upstream lets the composer hold several models at once and fans a single
	// prompt out to all of them, side by side. Hive does not sell that, and the
	// plus button that built such a list is gone from the markup below. Removing
	// only the button would have left the capability half present: a `models=`
	// query parameter, a folder carrying several model ids, and any conversation
	// saved back when the button existed all still arrive here as a longer list,
	// and the composer would then have drawn one model while dispatching to
	// several, which is worse than either behaviour on its own.
	//
	// `selectedModels` is bound all the way up through MessageInput and
	// Placeholder to Chat, so clamping it here is what the send path actually
	// reads, not merely what the chip renders.
	$: if (selectedModels.length !== 1) {
		selectedModels = [selectedModels[0] ?? ''];
	}

	$: if (selectedModels.length > 0 && $models.length > 0) {
		const _selectedModels = selectedModels.map((model) =>
			$models.map((m) => m.id).includes(model) ? model : ''
		);

		if (!equal(_selectedModels, selectedModels)) {
			selectedModels = _selectedModels;
		}
	}
</script>

<div class="flex flex-col w-full items-start">
	<div class="flex w-full max-w-fit">
		<div class="overflow-hidden w-full">
			<div class="max-w-full {($settings?.highContrastMode ?? false) ? 'm-1' : 'mr-1'}">
				<Selector
					id="0"
					placeholder={$i18n.t('Select a model')}
					items={$models.map((model) => ({
						value: model.id,
						label: model.name,
						model: model
					}))}
					{pinModelHandler}
					bind:value={selectedModels[0]}
				/>
			</div>
		</div>
	</div>
</div>

{#if showSetDefault}
	<div
		class="relative text-left mt-[1px] ml-1 text-[0.7rem] text-gray-600 dark:text-gray-400 font-primary"
	>
		<button on:click={saveDefaultModel}> {$i18n.t('Set as default')}</button>
	</div>
{/if}
