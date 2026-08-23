<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { onMount, getContext } from 'svelte';

	import { user, config, settings } from '$lib/stores';
	// Settings declutter: password change, OWUI API keys and the session JWT copy
	// are gone from this surface. Posture is SSO only (ENABLE_LOGIN_FORM false),
	// so a working set-password form here would bypass it, and an OWUI minted
	// key only competes with the real hk_ console keys.
	import { updateUserProfile, getSessionUser } from '$lib/apis/auths';
	import { generateInitialsImage } from '$lib/utils';
	import UserProfileImage from './Account/UserProfileImage.svelte';

	const i18n = getContext('i18n');

	export let saveHandler: Function;
	export let saveSettings: Function;

	let profileImageUrl = '';
	let name = '';

	let webhookUrl = '';

	const submitHandler = async () => {
		if (name !== $user?.name) {
			if (profileImageUrl === generateInitialsImage($user?.name) || profileImageUrl === '') {
				profileImageUrl = generateInitialsImage(name);
			}
		}

		if (webhookUrl !== $settings?.notifications?.webhook_url) {
			saveSettings({
				notifications: {
					...$settings.notifications,
					webhook_url: webhookUrl
				}
			});
		}

		// Bio, gender and date of birth are gone from this surface (settings
		// declutter): identity facts live with control-plane, not here. The keys
		// are omitted rather than nulled so a save never wipes stored values.
		const updatedUser = await updateUserProfile(localStorage.token, {
			name: name,
			profile_image_url: profileImageUrl
		}).catch((error) => {
			toast.error(`${error}`);
		});

		if (updatedUser) {
			// Get Session User Info
			const sessionUser = await getSessionUser(localStorage.token).catch((error) => {
				toast.error(`${error}`);
				return null;
			});

			await user.set(sessionUser);
			return true;
		}
		return false;
	};

	onMount(async () => {
		const user = await getSessionUser(localStorage.token).catch((error) => {
			toast.error(`${error}`);
			return null;
		});

		if (user) {
			name = user?.name ?? '';
			profileImageUrl = user?.profile_image_url ?? '';
		}

		webhookUrl = $settings?.notifications?.webhook_url ?? '';
	});
</script>

<div id="tab-account" class="flex flex-col h-full justify-between text-sm">
	<div class=" overflow-y-scroll max-h-[28rem] md:max-h-full">
		<div class="space-y-1">
			<div>
				<div class="text-base font-medium">{$i18n.t('Your Account')}</div>

				<div class="text-xs text-gray-500 mt-0.5">
					{$i18n.t('Manage your account information.')}
				</div>
			</div>

			<div class="flex space-x-5 my-4">
				<UserProfileImage bind:profileImageUrl user={$user} />

				<div class="flex flex-1 flex-col">
					<div class=" flex-1">
						<div class="flex flex-col w-full">
							<div class=" mb-1 text-xs font-medium">{$i18n.t('Name')}</div>

							<div class="flex-1">
								<input
									class="w-full text-sm dark:text-gray-300 bg-transparent outline-hidden"
									type="text"
									bind:value={name}
									aria-label={$i18n.t('Name')}
									required
									placeholder={$i18n.t('Enter your name')}
								/>
							</div>
						</div>
					</div>
				</div>
			</div>
		</div>

		{#if $config?.features?.enable_user_webhooks && ($user?.role === 'admin' || ($user?.permissions?.features?.webhooks ?? false))}
			<div class="mt-2">
				<div class="flex flex-col w-full">
					<div class=" mb-1 text-xs font-medium">{$i18n.t('Notification Webhook')}</div>

					<div class="flex-1">
						<input
							class="w-full text-sm outline-hidden"
							type="url"
							placeholder={$i18n.t('Enter your webhook URL')}
							aria-label={$i18n.t('Notification Webhook')}
							bind:value={webhookUrl}
							required
						/>
					</div>
				</div>
			</div>
		{/if}
	</div>

	<div class="flex justify-end pt-3 text-sm font-medium">
		<button
			class="px-3.5 py-1.5 text-sm font-medium bg-black hover:bg-gray-900 text-white dark:bg-white dark:text-black dark:hover:bg-gray-100 transition rounded-full"
			on:click={async () => {
				const res = await submitHandler();

				if (res) {
					saveHandler();
				}
			}}
		>
			{$i18n.t('Save')}
		</button>
	</div>
</div>
