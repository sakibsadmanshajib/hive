/*
 * Turns a skill save failure into a sentence the author can act on.
 *
 * Only one failure is rewritten. `skill.id` is unique across the whole
 * instance (backend/open_webui/models/skills.py) and the editor derives it by
 * slugifying the name, so two accounts naming a skill "Research" collide, and
 * upstream answers with ERROR_MESSAGES.ID_TAKEN: "Uh-oh! This id is already
 * registered. Please choose another id string." On this surface that sentence
 * misdirects twice over. It names a field the author never filled in, and the
 * skill holding the id is almost always one they cannot read, so they search
 * their own library, find nothing, and read it as a broken product rather than
 * a taken name.
 *
 * Issue #1397 is the real fix and PR #1437 carries it, scoping uniqueness to
 * the tenant. This message still matters after that lands: most accounts on
 * this deployment have no tenant group at all, so they share one namespace
 * whatever the scoping does with the accounts that do have one.
 *
 * Deliberately says nothing about WHICH skill or WHOSE. A global uniqueness
 * check leaks the bare fact that an id is taken and no wording can take that
 * back while the check exists, but naming the holder would turn an unavoidable
 * existence oracle into a real disclosure, on an instance where reads are
 * otherwise denied cross-account by ownership plus access grants.
 *
 * Every other failure passes through verbatim. A rewrite that catches more
 * than it means to is how a permission failure, a network failure and a
 * validation failure all come to read as the same shrug.
 */

// Anchored on the whole upstream sentence rather than a loose "already
// registered", so the sibling MODEL_ID_TAKEN message and any future one are
// not swept up by it.
const ID_TAKEN =
	/Uh-oh!\s*This id is already registered\.\s*Please choose another id string\./i;

const textOf = (error: unknown): string => {
	if (error instanceof Error) return error.message;
	if (typeof error === 'string') return error;
	if (error === null || error === undefined) return 'Could not save this skill.';
	return String(error);
};

export const skillSaveErrorMessage = (error: unknown, name: string): string => {
	const raw = textOf(error);
	if (!ID_TAKEN.test(raw)) return raw;

	const trimmed = (name ?? '').trim();
	const subject = trimmed === '' ? 'That name' : `The name “${trimmed}”`;

	return (
		`${subject} is already taken. Skill names have to be unique across this whole ` +
		'instance, and the skill holding it may belong to another account you cannot see, ' +
		'so it will not appear in your own list. Choose a different name, or edit the id ' +
		'field to something unused.'
	);
};
