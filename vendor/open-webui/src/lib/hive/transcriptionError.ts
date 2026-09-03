// What to tell someone when a transcription request fails (issue #1627).
//
// The client used to read `err.detail` and nothing else. That is the shape
// Open WebUI's own backend raises, so a rejected upload from the API surfaced
// correctly and every other failure vanished: `error` stayed undefined, the
// `if (error) throw` never fired, the function returned null, and the caller
// treated a failure as "nothing was said".
//
// Two failures reach that path with no `detail` at all. A transport error, and
// the AbortSignal timeout the call now carries, which rejects with a
// DOMException. Both were silent, and the timeout is the one that matters most:
// it is the case where the microphone has been suppressed for two minutes
// waiting, so the call looks deaf and the interface says nothing about why.

export interface TranscriptionFailure {
	/** Open WebUI's own error shape, which carries the upstream message. */
	detail?: unknown;
	/** DOMException name. `TimeoutError` is the AbortSignal timeout. */
	name?: unknown;
	message?: unknown;
}

/**
 * The message to show for a failed transcription. Never empty, because an
 * empty one is how this failed silently in the first place.
 */
export const transcriptionErrorMessage = (err: TranscriptionFailure | null | undefined): string => {
	if (typeof err?.detail === 'string') {
		// A whitespace-only detail is not a message. Falling through to the
		// generic text below is better than showing a blank toast, which is
		// the same silence this function exists to end.
		const detail = err.detail.trim();
		if (detail) {
			return detail;
		}
	} else if (err?.detail !== undefined && err?.detail !== null) {
		// A structured detail is still a real answer from the backend, and
		// String() on an object is "[object Object]", which tells nobody
		// anything. Serialize it, and fall back only if it cannot be.
		try {
			const encoded = JSON.stringify(err.detail);
			if (encoded && encoded !== '{}' && encoded !== 'null') {
				return encoded;
			}
		} catch {
			// Circular or otherwise unserializable; the generic text below.
		}
	}
	if (err?.name === 'TimeoutError') {
		return 'Transcription timed out. Please try again.';
	}
	const message = typeof err?.message === 'string' ? err.message.trim() : '';
	if (message) {
		return message;
	}
	return 'Transcription failed. Please try again.';
};
