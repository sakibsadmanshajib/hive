/*
 * Bounded helpers for the /artifacts index page (issue #1110).
 *
 * Dependency-free on purpose: everything here must run inside
 * scripts/test-owui-hive-frontend.sh's scratch tree, which copies only
 * src/lib/hive plus a few text fixtures, so this module imports nothing from
 * $lib. The page supplies the real chat fetch; this module owns the timeout
 * whose whole job is to guarantee the route terminates in a visible state
 * instead of an eternal spinner.
 */

export type ArtifactEntry = {
	chatId: string;
	chatTitle: string;
	artifactType: 'iframe' | 'svg';
	content: string;
};

/**
 * Promise.race the work against a timer so a hung backend becomes a visible
 * error after a bounded wait instead of an eternal spinner. The timer always
 * clears when the work settles first, so a fast load never leaves a dangling
 * timer.
 */
export const withTimeout = async <T>(work: Promise<T>, timeoutMs: number): Promise<T> => {
	let timer: ReturnType<typeof setTimeout>;
	const timeout = new Promise<never>((_, reject) => {
		timer = setTimeout(() => reject(new Error('timeout')), timeoutMs);
	});
	try {
		return await Promise.race([work, timeout]);
	} finally {
		clearTimeout(timer!);
	}
};

/**
 * Same document shape Chat.svelte builds for the in-chat Artifacts panel: one
 * HTML/CSS/JS group becomes one sandboxed iframe document.
 */
export const buildIframeDoc = (group: { html: string; css: string; js: string }): string =>
	`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<${''}style>
body { background-color: white; }
${group.css}
</${''}style>
</head>
<body>
${group.html}
<${''}script>
${group.js}
</${''}script>
</body>
</html>`;
