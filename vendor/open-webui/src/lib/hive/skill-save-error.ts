/*
 * Turns a skill save failure into a sentence the author can act on.
 *
 * Only one failure is rewritten. `skill.id` is unique across the instance
 * (backend/open_webui/models/skills.py) and upstream answers a collision with
 * ERROR_MESSAGES.ID_TAKEN: "Uh-oh! This id is already registered. Please
 * choose another id string." Two things are wrong with that on this surface.
 * It reads as a schema complaint rather than an instruction, and it says
 * nothing about why the author cannot find the offending skill: the one
 * holding the id is usually one they cannot read, so they search their own
 * library, find nothing, and conclude the product is broken.
 *
 * It names the ID and not the name, deliberately. The editor derives the id
 * from the name only while creating a fresh skill:
 *
 *     $: if (!edit && !clone && name) { id = slugify(name); }
 *
 * so on three of the four routes into this error the link is already broken. A
 * clone carries `${id}_clone` and freezes it. A markdown import takes the id
 * from frontmatter, independently of the displayed name. A hand-edited id
 * stops tracking the moment it is touched. And even on the plain path
 * `slugify` strips punctuation, so "Research!" and "Research" are one id and
 * an author told to change the NAME can edit it forever without moving the
 * thing that actually collided. The id is what collided, the editor shows it,
 * and pointing at it is true on all four routes.
 *
 * It asserts no scope, which is why it survived PR #1437 landing under it.
 * "Unique across this whole instance" was true when this was written and false
 * a few hours later: `apply_skill_tenant_scope_router_patch.py` now prefixes
 * the id with a scope, and that scope is the caller's tenant group, or their
 * own user id when they have none, or nothing at all for a platform admin.
 * Any scope word would therefore have been wrong for some population of
 * accounts on this deployment, and most accounts here have no tenant group.
 *
 * That same patch is why the sentence hedges on WHOSE skill holds the id
 * rather than asserting it. After #1437 a collision means a duplicate inside
 * the caller's own scope, so for the common ungrouped account it is one of
 * their own skills, sitting in their own list. For a tenant-grouped member it
 * is a peer's, which ownership plus access grants may well hide from them. For
 * an admin, on the unscoped namespace, it is anyone's. "One of your own, or
 * one you do not have access to see" is the only phrasing true in all three.
 *
 * It never names WHICH skill or WHOSE. A uniqueness check leaks the bare fact
 * that an id is taken and no wording takes that back while the check exists,
 * but naming the holder would turn an unavoidable existence oracle into a real
 * disclosure, on an instance where reads are otherwise denied cross-account by
 * ownership plus access grants in application logic, with no row level
 * security behind them.
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

const TAIL =
	'It may be one of your own skills, or one you do not have access to see. ' +
	'Edit the id field to something unused.';

/**
 * The subset of i18next's `t` this module uses. Passing `$i18n.t` keeps these
 * sentences translatable, which matters because the success toast beside them
 * already is and bn-BD is the first market. Defaults to identity so the unit
 * tests, and any caller with no i18n context, still get English.
 */
export type Translate = (key: string, vars?: Record<string, string>) => string;

const identity: Translate = (key, vars) =>
	vars ? key.replace(/{{(\w+)}}/g, (_, name) => vars[name] ?? '') : key;

const textOf = (error: unknown, t: Translate): string => {
	if (error instanceof Error) return error.message;
	if (typeof error === 'string') return error;
	if (error === null || error === undefined) return t('Could not save this skill.');
	return String(error);
};

export const skillSaveErrorMessage = (
	error: unknown,
	id: string,
	t: Translate = identity
): string => {
	const raw = textOf(error, t);
	if (!ID_TAKEN.test(raw)) return raw;

	const trimmed = (id ?? '').trim();
	return trimmed === ''
		? t(`That id is already in use. ${TAIL}`)
		: t(`The id “{{id}}” is already in use. ${TAIL}`, { id: trimmed });
};
