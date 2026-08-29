<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { goto } from '$app/navigation';
	import { skills } from '$lib/stores';
	import { onMount, getContext } from 'svelte';

	const i18n = getContext('i18n');

	import { createNewSkill, getSkills } from '$lib/apis/skills';
	import SkillEditor from '$lib/components/workspace/Skills/SkillEditor.svelte';
	import { skillSaveErrorMessage } from '$lib/hive/skill-save-error';

	let skill: {
		name: string;
		id: string;
		description: string;
		content: string;
		is_active: boolean;
		access_grants: any[];
	} | null = null;

	let clone = false;

	const onSubmit = async (_skill) => {
		const res = await createNewSkill(localStorage.token, _skill).catch((error) => {
			// The id is derived from the name and is unique across the whole
			// instance, so the commonest failure here is a collision with a
			// skill this account cannot read. Upstream's wording for that names
			// a field the author never filled in and sends them looking for a
			// skill that is not in their library. See lib/hive/skill-save-error.
			toast.error(skillSaveErrorMessage(error, _skill?.name ?? ''));
			return null;
		});

		if (res) {
			toast.success($i18n.t('Skill created successfully'));
			await skills.set(await getSkills(localStorage.token));
			await goto('/skills');
		}
	};

	onMount(async () => {
		if (sessionStorage.skill) {
			const _skill = JSON.parse(sessionStorage.skill);
			sessionStorage.removeItem('skill');

			clone = true;
			skill = {
				name: _skill.name || 'Skill',
				id: _skill.id || '',
				description: _skill.description || '',
				content: _skill.content || '',
				is_active: _skill.is_active ?? true,
				access_grants: _skill.access_grants !== undefined ? _skill.access_grants : []
			};
		}
	});
</script>

{#key skill}
	<SkillEditor {skill} {onSubmit} {clone} />
{/key}
