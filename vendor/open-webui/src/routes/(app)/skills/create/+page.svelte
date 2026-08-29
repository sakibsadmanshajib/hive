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
		let reported = false;

		const res = await createNewSkill(localStorage.token, _skill).catch((error) => {
			// The commonest failure here is an id collision, and upstream's
			// wording for it reads as a schema complaint about a field the
			// author never filled in. See lib/hive/skill-save-error for why
			// the message names the id rather than the name (a clone, an
			// import and a hand-edited id all decouple them) and why it claims
			// no scope for who holds it (PR #1437 made that the caller's own
			// tenant, or their own account, or nothing, depending on who they
			// are).
			reported = true;
			toast.error(skillSaveErrorMessage(error, _skill?.id ?? '', $i18n.t));
			return null;
		});

		if (res) {
			toast.success($i18n.t('Skill created successfully'));
			await skills.set(await getSkills(localStorage.token));
			await goto('/skills');
		} else if (!reported) {
			// createNewSkill rethrows only when the error body carried a
			// `detail` string, so a network failure or a non-JSON error page
			// resolves it with null and the catch above never runs. Without
			// this arm the author clicks Save and nothing happens at all.
			toast.error(skillSaveErrorMessage(undefined, _skill?.id ?? '', $i18n.t));
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
