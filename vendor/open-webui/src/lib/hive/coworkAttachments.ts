/**
 * Composer attachments for a Cowork run (issue #1065).
 *
 * Work mode used to refuse every attachment outright, with the honest reason
 * that the run backend accepted only a pack and a prompt. It accepts documents
 * now: `POST /v1/agent/tasks` carries them, and the launcher writes them into
 * the sandbox's working directory before the agent starts.
 *
 * The text travels inline rather than as a file id, and that is deliberate.
 * The sandbox holds no Hive credential and has no network route to the storage
 * a chat attachment lives in, so something has to hand it the bytes; the
 * browser that uploaded them is the one party already authorized to read them.
 * No new read path, no new permission, and nothing about who can see whose
 * documents changes.
 */

export type CoworkAttachment = {
	name: string;
	content: string;
};

/** One entry of the composer's `files` array, narrowed to what is read here. */
export type ComposerFile = {
	type?: string;
	id?: string | null;
	name?: string;
	content?: string;
	file?: { data?: { content?: string | null } | null } | null;
};

/**
 * ponytail: an inline cap, not a general file transfer. It comfortably holds
 * an ordinary document (a hundred page report extracts to well under this) and
 * it is the honest ceiling of carrying text on the create request. The upgrade
 * path, when a run needs a 25 MB PDF verbatim, is for the launcher to fetch the
 * document itself, which needs a credential and a route it does not have.
 *
 * Kept in step with maxAttachmentBytes and maxAttachments in
 * apps/edge-api/internal/agenttask/handler.go, which is where the refusal is
 * actually enforced. This copy exists so the person is told before the send
 * rather than by a 400.
 */
export const COWORK_ATTACHMENT_MAX_TOTAL_BYTES = 256 * 1024;
export const COWORK_ATTACHMENT_MAX_COUNT = 5;

export type CoworkAttachmentFailure =
	| { ok: false; reason: 'unsupported'; name: string }
	| { ok: false; reason: 'empty'; name: string }
	| { ok: false; reason: 'too-many' }
	| { ok: false; reason: 'too-large' };

export type CoworkAttachmentResult =
	| { ok: true; attachments: CoworkAttachment[] }
	| CoworkAttachmentFailure;

/**
 * Whether a composer entry is a document a run can be given.
 *
 * `file` is an ordinary upload; `text` is the same upload in a temporary chat,
 * where the extraction happens in the browser and never reaches the server.
 * Everything else the plus menu can add (an image, a collection, another chat,
 * a note, a folder) is a reference to something the sandbox cannot resolve, so
 * it keeps the refusal it always had rather than being silently dropped.
 */
export const isCoworkAttachable = (file: ComposerFile | null | undefined): boolean =>
	file?.type === 'file' || file?.type === 'text';

/**
 * Reduce an uploaded file's name to a bare file name, or return '' for
 * anything that is not one. The launcher refuses the same shapes, being the
 * process that turns a name into a path; this is what keeps the person from
 * finding that out through a failed run.
 *
 * A space is left alone: it is legal in a file name and rejecting it would
 * refuse most real documents.
 */
export const attachmentFileName = (raw: string | null | undefined): string => {
	const name = (raw ?? '').trim();
	if (name === '' || name === '.' || name === '..') {
		return '';
	}
	if (name.length > 255) {
		return '';
	}
	if (name.includes('/') || name.includes('\\')) {
		return '';
	}
	for (let i = 0; i < name.length; i += 1) {
		// Control characters, NUL included, are not file names.
		if (name.charCodeAt(i) < 0x20) {
			return '';
		}
	}
	return name;
};

const byteLength = (value: string): number => new TextEncoder().encode(value).length;

/**
 * Build the attachment list for a run, reading each file's extracted text.
 *
 * `readContent` is injected rather than imported so this stays testable
 * without a server: Chat.svelte passes a reader over `GET /api/v1/files/{id}`,
 * which is the authoritative copy of the text Open WebUI extracted at upload.
 * A file whose text is not there is refused by name, because handing the agent
 * an empty document and letting it answer from nothing is exactly the silent
 * failure this issue is about.
 */
export const collectCoworkAttachments = async (
	files: ComposerFile[] | null | undefined,
	readContent: (id: string) => Promise<string>
): Promise<CoworkAttachmentResult> => {
	const items = files ?? [];
	if (items.length === 0) {
		return { ok: true, attachments: [] };
	}
	if (items.length > COWORK_ATTACHMENT_MAX_COUNT) {
		return { ok: false, reason: 'too-many' };
	}

	const attachments: CoworkAttachment[] = [];
	let total = 0;
	for (const item of items) {
		const name = attachmentFileName(item?.name);
		if (!isCoworkAttachable(item) || name === '') {
			return { ok: false, reason: 'unsupported', name: (item?.name ?? '').trim() };
		}
		// The in-browser extraction a temporary chat produces is already on the
		// item; an ordinary upload is read back from the server.
		let content = item?.content ?? item?.file?.data?.content ?? '';
		if (content === '' && item?.id) {
			content = (await readContent(item.id)) ?? '';
		}
		if (content === '') {
			return { ok: false, reason: 'empty', name };
		}
		total += byteLength(content);
		if (total > COWORK_ATTACHMENT_MAX_TOTAL_BYTES) {
			return { ok: false, reason: 'too-large' };
		}
		attachments.push({ name, content });
	}
	return { ok: true, attachments };
};
