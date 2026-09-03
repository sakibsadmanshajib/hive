<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { createEventDispatcher, onMount, getContext } from 'svelte';
	import { getLanguages, changeLanguage } from '$lib/i18n';
	const dispatch = createEventDispatcher();

	import { config, models, settings, theme, user } from '$lib/stores';

	const i18n = getContext('i18n');

	import AdvancedParams from './Advanced/AdvancedParams.svelte';
	import Textarea from '$lib/components/common/Textarea.svelte';
	/*
	 * Issue #1363. Custom instructions are Hive state, not Open WebUI state.
	 * Upstream's "System Prompt" field, which this replaces, wrote
	 * `$settings.system` into Open WebUI's own database and was delivered by
	 * the browser attaching a system message to each outgoing request. Both
	 * halves were wrong for this product: the value is invisible to every
	 * client that is not this browser, it does not survive a volume reset, and
	 * `.wolf/decisions.md` D-044 puts the source of truth in the backend Hive
	 * owns. The Hive control below reads and writes edge-api, which is also
	 * what injects the text into every chat turn, so what a person sees here
	 * is what shapes their replies.
	 */
	import {
		MAX_INSTRUCTIONS_LENGTH,
		getCustomInstructions,
		saveCustomInstructions
	} from '$lib/hive/customInstructions';
	export let saveSettings: Function;
	export let getModels: Function;

	// General
	/*
	 * Three states, and they are the whole set: System, Light, Dark.
	 *
	 * Upstream also shipped `oled-dark` and an easter-egg `her`. Neither is
	 * Hive's to offer. `oled-dark` overwrites four `--color-gray-*` custom
	 * properties with raw hex at runtime, which is precisely the twelve-value
	 * ramp the brand palette is applied through (vendor/open-webui/src/tailwind.css),
	 * so choosing it silently discarded the warm neutral ramp for a pure-black
	 * one nobody designed and nothing measured for contrast. `her` set the meta
	 * theme colour to a maroon that appears nowhere in the token layer.
	 *
	 * A stored value of either still resolves: applyTheme maps an unknown or
	 * legacy theme onto dark or light below, so an account that picked one
	 * before this change lands on the nearest real theme rather than on a blank
	 * select.
	 */
	let themes = ['dark', 'light'];
	let selectedTheme = 'system';

	let languages: Awaited<ReturnType<typeof getLanguages>> = [];
	let lang = $i18n.language;
	let notificationEnabled = false;

	// Custom instructions (#1363). `instructionsLoaded` gates the save, and it
	// is set only when the read actually SUCCEEDED, not merely when it
	// finished. An unreadable load leaves it false, `saveHandler` skips the
	// PUT entirely, and the box stays disabled, so a transient 502 on load can
	// never post an empty string over text the person still has. Empty content
	// deletes the row, so that mistake is not a stale render, it is deletion.
	let customInstructions = '';
	let instructionsLoaded = false;
	let instructionsUnreadable = false;
	let savingInstructions = false;

	let showAdvanced = false;

	const toggleNotification = async () => {
		const permission = await Notification.requestPermission();

		if (permission === 'granted') {
			notificationEnabled = !notificationEnabled;
			saveSettings({ notificationEnabled: notificationEnabled });
		} else {
			toast.error(
				$i18n.t(
					'Response notifications cannot be activated as the website permissions have been denied. Please visit your browser settings to grant the necessary access.'
				)
			);
		}
	};

	let params = {
		// Advanced
		stream_response: null,
		stream_delta_chunk_size: null,
		function_calling: null,
		reasoning_tags: null,
		seed: null,
		temperature: null,
		reasoning_effort: null,
		logit_bias: null,
		frequency_penalty: null,
		presence_penalty: null,
		repeat_penalty: null,
		repeat_last_n: null,
		mirostat: null,
		mirostat_eta: null,
		mirostat_tau: null,
		top_k: null,
		top_p: null,
		min_p: null,
		stop: null,
		tfs_z: null,
		num_ctx: null,
		num_batch: null,
		num_keep: null,
		max_tokens: null,
		use_mmap: null,
		use_mlock: null,
		num_thread: null,
		num_gpu: null,
		think: null,
		format: null,
		keep_alive: null
	};

	const saveHandler = async () => {
		// Instructions go to Hive's own backend first, and a failure there stops
		// the save rather than being swallowed: a pane that closes on a failed
		// write tells the person their instructions were stored when they were
		// not, and they will not find out until an answer ignores them.
		if (instructionsLoaded) {
			savingInstructions = true;
			try {
				customInstructions = await saveCustomInstructions(customInstructions);
			} catch (error) {
				toast.error(error instanceof Error ? error.message : String(error));
				savingInstructions = false;
				return;
			}
			savingInstructions = false;
		}

		saveSettings({
			params: {
				stream_response: params.stream_response !== null ? params.stream_response : undefined,
				stream_delta_chunk_size:
					params.stream_delta_chunk_size !== null ? params.stream_delta_chunk_size : undefined,
				function_calling: params.function_calling !== null ? params.function_calling : undefined,
				reasoning_tags: params.reasoning_tags !== null ? params.reasoning_tags : undefined,
				seed: (params.seed !== null ? params.seed : undefined) ?? undefined,
				stop: params.stop ? params.stop.split(',').filter((e) => e) : undefined,
				temperature: params.temperature !== null ? params.temperature : undefined,
				reasoning_effort: params.reasoning_effort !== null ? params.reasoning_effort : undefined,
				logit_bias: params.logit_bias !== null ? params.logit_bias : undefined,
				frequency_penalty: params.frequency_penalty !== null ? params.frequency_penalty : undefined,
				presence_penalty: params.presence_penalty !== null ? params.presence_penalty : undefined,
				repeat_penalty: params.repeat_penalty !== null ? params.repeat_penalty : undefined,
				repeat_last_n: params.repeat_last_n !== null ? params.repeat_last_n : undefined,
				mirostat: params.mirostat !== null ? params.mirostat : undefined,
				mirostat_eta: params.mirostat_eta !== null ? params.mirostat_eta : undefined,
				mirostat_tau: params.mirostat_tau !== null ? params.mirostat_tau : undefined,
				top_k: params.top_k !== null ? params.top_k : undefined,
				top_p: params.top_p !== null ? params.top_p : undefined,
				min_p: params.min_p !== null ? params.min_p : undefined,
				tfs_z: params.tfs_z !== null ? params.tfs_z : undefined,
				num_ctx: params.num_ctx !== null ? params.num_ctx : undefined,
				num_batch: params.num_batch !== null ? params.num_batch : undefined,
				num_keep: params.num_keep !== null ? params.num_keep : undefined,
				max_tokens: params.max_tokens !== null ? params.max_tokens : undefined,
				use_mmap: params.use_mmap !== null ? params.use_mmap : undefined,
				use_mlock: params.use_mlock !== null ? params.use_mlock : undefined,
				num_thread: params.num_thread !== null ? params.num_thread : undefined,
				num_gpu: params.num_gpu !== null ? params.num_gpu : undefined,
				think: params.think !== null ? params.think : undefined,
				keep_alive: params.keep_alive !== null ? params.keep_alive : undefined,
				format: params.format !== null ? params.format : undefined,
				...(params.custom_params && Object.keys(params.custom_params).length > 0
					? { custom_params: params.custom_params }
					: {})
			}
		});
		dispatch('save');
	};

	onMount(async () => {
		selectedTheme = localStorage.theme ?? 'system';

		languages = await getLanguages();

		if (!$config?.features?.enable_easter_eggs) {
			languages = languages.filter((l) => l.code !== 'dg-DG');
		}

		notificationEnabled = $settings.notificationEnabled ?? false;

		// Never throws. null means the read failed, which is NOT the same as an
		// empty box: the box stays disabled and the save skips this field, so
		// nothing overwrites what could not be read.
		const stored = await getCustomInstructions();
		instructionsUnreadable = stored === null;
		customInstructions = stored ?? '';
		instructionsLoaded = stored !== null;

		params = { ...params, ...$settings.params };
		params.stop = $settings?.params?.stop ? ($settings?.params?.stop ?? []).join(',') : null;
	});

	const applyTheme = (_theme: string) => {
		// Legacy values are folded onto the theme they most resembled, so an
		// account that stored one before the control was reduced still resolves
		// to something real.
		let themeToApply = _theme === 'oled-dark' ? 'dark' : _theme === 'her' ? 'light' : _theme;

		if (_theme === 'system') {
			themeToApply = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
		}

		/*
		 * Upstream wrote four neutral hex values onto --color-gray-800, -850,
		 * -900 and -950 as INLINE styles on the root element every time dark was
		 * applied. Those four are the entire dark half of the ramp the brand
		 * palette is defined in (vendor/open-webui/src/tailwind.css), and an
		 * inline style beats any stylesheet, so choosing Dark silently threw the
		 * warm charcoal away and rendered a neutral grey product nobody
		 * designed. That is why the dark register looked unimplemented rather
		 * than wrong: it was implemented, and then overwritten at runtime.
		 *
		 * Deleted rather than re-pointed at the tokens, because the tokens
		 * already hold these values and the stylesheet already applies them.
		 *
		 * `data-theme` is set alongside the class because the two halves of the
		 * product key off different things: upstream's own utilities read the
		 * `dark` class, and the Hive token layer defines its dark register under
		 * `[data-theme="dark"]` and `prefers-color-scheme`. Without this line, a
		 * person on a light operating system who chooses Dark gets upstream's
		 * dark surfaces with Hive's light navigation sitting on top of them.
		 */
		document.documentElement.dataset.theme = themeToApply;

		themes
			.filter((e) => e !== themeToApply)
			.forEach((e) => {
				e.split(' ').forEach((e) => {
					document.documentElement.classList.remove(e);
				});
			});

		themeToApply.split(' ').forEach((e) => {
			document.documentElement.classList.add(e);
		});

		const metaThemeColor = document.querySelector('meta[name="theme-color"]');
		if (metaThemeColor) {
			if (_theme.includes('system')) {
				const systemTheme = window.matchMedia('(prefers-color-scheme: dark)').matches
					? 'dark'
					: 'light';
				console.log('Setting system meta theme color: ' + systemTheme);
				metaThemeColor.setAttribute('content', systemTheme === 'light' ? '#ffffff' : '#171717');
			} else {
				console.log('Setting meta theme color: ' + _theme);
				metaThemeColor.setAttribute('content', themeToApply === 'dark' ? '#171717' : '#ffffff');
			}
		}

		if (typeof window !== 'undefined' && window.applyTheme) {
			window.applyTheme();
		}

		console.log(_theme);
	};

	const themeChangeHandler = (_theme: string) => {
		theme.set(_theme);
		localStorage.setItem('theme', _theme);
		applyTheme(_theme);
	};
</script>

<div class="flex flex-col h-full justify-between text-sm" id="tab-general">
	<div class="  overflow-y-scroll max-h-[28rem] md:max-h-full">
		<div class="">
			<!-- hive: was "WebUI Settings" (parity review finding, this section
			read as stock Open WebUI branding rather than Hive's own product).
			The key equals its own English text, the i18next convention this
			file already relies on, so every locale renders something
			immediately. That is a fallback, not a translation: the old key was
			translated in bn-BD and 61 other locales and the rename drops those
			translations, so the new key is added to en-US and translated in
			bn-BD (the first market) in this same change. The remaining locales
			fall back to English until their own translators reach it, which is
			how every other untranslated key in this fork already behaves. -->
			<div class=" mb-1 text-sm font-medium">{$i18n.t('Chat Preferences')}</div>

			<div class="flex w-full justify-between">
				<div class=" self-center text-xs font-medium">{$i18n.t('Theme')}</div>
				<div class="flex items-center relative">
					<select
						class="w-fit pr-8 rounded-sm py-2 px-2 text-xs bg-transparent text-right {$settings.highContrastMode
							? ''
							: 'outline-hidden'}"
						bind:value={selectedTheme}
						placeholder={$i18n.t('Select a theme')}
						on:change={() => themeChangeHandler(selectedTheme)}
					>
						<option value="system">⚙️ {$i18n.t('System')}</option>
						<option value="light">☀️ {$i18n.t('Light')}</option>
						<option value="dark">🌑 {$i18n.t('Dark')}</option>
					</select>
				</div>
			</div>

			<div class=" flex w-full justify-between">
				<div class=" self-center text-xs font-medium">{$i18n.t('Language')}</div>
				<div class="flex items-center relative">
					<select
						class="w-fit pr-8 rounded-sm py-2 px-2 text-xs bg-transparent text-right {$settings.highContrastMode
							? ''
							: 'outline-hidden'}"
						bind:value={lang}
						placeholder={$i18n.t('Select a language')}
						on:change={(e) => {
							changeLanguage(lang);
						}}
					>
						{#each languages as language}
							<option value={language['code']}>{language['title']}</option>
						{/each}
					</select>
				</div>
			</div>
			{#if $i18n.language === 'en-US' && !($config?.license_metadata ?? false)}
				<div
					class="mb-2 text-xs {($settings?.highContrastMode ?? false)
						? 'text-gray-800 dark:text-gray-100'
						: 'text-gray-400 dark:text-gray-500'}"
				>
					Couldn't find your language?
					<a
						class="font-medium underline {($settings?.highContrastMode ?? false)
							? 'text-gray-700 dark:text-gray-200'
							: 'text-gray-300'}"
						href="https://github.com/open-webui/open-webui/blob/main/docs/CONTRIBUTING.md#-translations-and-internationalization"
						target="_blank"
					>
						Help us translate Hive Chat!
					</a>
				</div>
			{/if}

			<div>
				<div class=" py-0.5 flex w-full justify-between">
					<div class=" self-center text-xs font-medium">{$i18n.t('Notifications')}</div>

					<button
						class="p-1 px-3 text-xs flex rounded-sm transition"
						on:click={() => {
							toggleNotification();
						}}
						type="button"
						role="switch"
						aria-checked={notificationEnabled}
					>
						{#if notificationEnabled === true}
							<span class="ml-2 self-center">{$i18n.t('On')}</span>
						{:else}
							<span class="ml-2 self-center">{$i18n.t('Off')}</span>
						{/if}
					</button>
				</div>
			</div>
		</div>

		<!--
			Custom instructions (#1363). Deliberately NOT behind
			`permissions.chat.system_prompt`, which gated the upstream control
			this replaces. That permission exists to decide who may override a
			model's system prompt, which is an operator concern. Telling the
			assistant how to address you is not that: it is the single most
			ordinary setting a chat product has, every competitor ships it on
			the free tier, and a deployment that switched the operator gate off
			would silently take it away from everyone.
		-->
		<hr class="border-gray-100/30 dark:border-gray-850/30 my-3" />

		<div>
			<div class="flex justify-between items-center">
				<div class=" my-2.5 text-sm font-medium">{$i18n.t('Custom instructions')}</div>
				<div
					class=" text-xs {customInstructions.length > MAX_INSTRUCTIONS_LENGTH
						? 'text-red-500'
						: 'text-gray-400 dark:text-gray-500'}"
				>
					{customInstructions.length}/{MAX_INSTRUCTIONS_LENGTH}
				</div>
			</div>
			<div class=" mb-2 text-xs text-gray-500 dark:text-gray-400">
				{$i18n.t('Applied to every conversation. Tell the assistant how you want it to respond.')}
			</div>
			{#if instructionsUnreadable}
				<!--
					Said out loud rather than rendered as an empty box. An empty box
					would read as "you have written none", and the person's next Save
					would have been asked to make that true.
				-->
				<div class=" mb-2 text-xs text-red-500" role="status">
					{$i18n.t(
						'Your custom instructions could not be loaded, so they are not editable right now. Anything you have saved before is unchanged. Reopen Settings to try again.'
					)}
				</div>
			{/if}
			<Textarea
				bind:value={customInstructions}
				disabled={!instructionsLoaded || savingInstructions}
				className={'w-full text-sm outline-hidden resize-vertical' +
					($settings.highContrastMode
						? ' p-2.5 border-2 border-gray-300 dark:border-gray-700 rounded-lg bg-transparent text-gray-900 dark:text-gray-100 focus:ring-1 focus:ring-blue-500 focus:border-blue-500 overflow-y-hidden'
						: '  dark:text-gray-300 ')}
				rows="4"
				placeholder={$i18n.t('For example: be concise, and always show your reasoning.')}
			/>
		</div>

		{#if $user?.role === 'admin' || (($user?.permissions.chat?.controls ?? true) && ($user?.permissions.chat?.params ?? true))}
			<div class="mt-2 space-y-3 pr-1.5">
				<div class="flex justify-between items-center text-sm">
					<div class="  font-medium">{$i18n.t('Advanced Parameters')}</div>
					<button
						class=" text-xs font-medium {($settings?.highContrastMode ?? false)
							? 'text-gray-800 dark:text-gray-100'
							: 'text-gray-400 dark:text-gray-500'}"
						type="button"
						aria-expanded={showAdvanced}
						on:click={() => {
							showAdvanced = !showAdvanced;
						}}>{showAdvanced ? $i18n.t('Hide') : $i18n.t('Show')}</button
					>
				</div>

				{#if showAdvanced}
					<AdvancedParams admin={$user?.role === 'admin'} custom={true} bind:params />
				{/if}
			</div>
		{/if}
	</div>

	<div class="flex justify-end pt-3 text-sm font-medium">
		<button
			class="px-3.5 py-1.5 text-sm font-medium bg-black hover:bg-gray-900 text-white dark:bg-white dark:text-black dark:hover:bg-gray-100 transition rounded-full"
			on:click={() => {
				saveHandler();
			}}
		>
			{$i18n.t('Save')}
		</button>
	</div>
</div>
