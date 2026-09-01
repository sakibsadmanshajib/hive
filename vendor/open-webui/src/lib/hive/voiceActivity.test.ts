import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import {
	DEFAULT_VOICE_ACTIVITY_CONFIG,
	createVoiceActivityState,
	speechThreshold,
	stepVoiceActivity,
	type VoiceActivityConfig
} from './voiceActivity';

// Issue #1627. Voice mode never stopped listening. The call overlay decided
// "someone is speaking" with `domainData.some((value) => value > 0)`, which is
// true the moment any single frequency bin clears the analyser's -55 dBFS
// floor. That is not a speech threshold, it is an "is the microphone on"
// check: in any real room it holds on nearly every frame, so the silence
// timer was reset forever, the recorder was never stopped, and the microphone
// stayed open for as long as the overlay did.
//
// The decision lives here rather than inside the component so it can be
// exercised frame by frame. The component keeps only the wiring.

const cfg = DEFAULT_VOICE_ACTIVITY_CONFIG;

// A frame of a quiet-but-not-silent room: well under the speech threshold,
// comfortably over the analyser's old any-bin trigger.
const ROOM_RMS = 0.006;
const SPEECH_RMS = 0.12;

const feed = (
	frames: Array<{ rms: number; at: number }>,
	config: VoiceActivityConfig = cfg
) => {
	let state = createVoiceActivityState();
	const actions: string[] = [];
	for (const frame of frames) {
		const next = stepVoiceActivity(state, frame.rms, frame.at, config);
		state = next.state;
		actions.push(next.action);
	}
	return { state, actions };
};

// 60 fps, which is what requestAnimationFrame gives the overlay.
const frames = (rms: number, count: number, from = 0, stepMs = 16) =>
	Array.from({ length: count }, (_, i) => ({ rms, at: from + i * stepMs }));

describe('speech threshold', () => {
	it('never drops below the absolute minimum, however quiet the room', () => {
		const state = { ...createVoiceActivityState(), noiseFloor: 0 };
		expect(speechThreshold(state, cfg)).toBe(cfg.minRms);
	});

	it('rises with the tracked noise floor but stays bounded', () => {
		const quiet = speechThreshold({ ...createVoiceActivityState(), noiseFloor: 0 }, cfg);
		const noisy = speechThreshold(
			{ ...createVoiceActivityState(), noiseFloor: cfg.minRms },
			cfg
		);
		expect(noisy).toBeGreaterThan(quiet);
		expect(noisy).toBeLessThanOrEqual(cfg.minRms * cfg.floorMultiplier);
	});
});

describe('idle', () => {
	it('treats room noise as silence for a full minute', () => {
		// The defect, stated as a test: 3750 frames is about a minute of the
		// overlay sitting open in an ordinary room. Not one of them may start
		// an utterance, and none may report anything but idle.
		const { state, actions } = feed(frames(ROOM_RMS, 3750));
		expect(new Set(actions)).toEqual(new Set(['idle']));
		expect(state.speechStartedAt).toBeNull();
	});

	it('ignores a non-finite level instead of hearing speech in it', () => {
		const { state, actions } = feed([
			{ rms: Number.NaN, at: 0 },
			{ rms: Number.POSITIVE_INFINITY, at: 16 }
		]);
		expect(actions).toEqual(['idle', 'idle']);
		expect(state.speechStartedAt).toBeNull();
		expect(Number.isFinite(state.noiseFloor)).toBe(true);
	});

	it('adapts to a louder room without letting the threshold run away', () => {
		// A room noisier than the absolute minimum, but still not speech. The
		// threshold has to climb over it, and stop climbing: a floor that
		// chased itself upwards would end up deaf to the user.
		const noisy = feed(frames(0.014, 2000)).state;
		expect(noisy.noiseFloor).toBeGreaterThan(cfg.minRms / 2);
		expect(noisy.noiseFloor).toBeLessThanOrEqual(cfg.minRms);
		expect(speechThreshold(noisy, cfg)).toBeLessThanOrEqual(cfg.minRms * cfg.floorMultiplier);

		// Same level, two rooms: loud enough to be speech in a quiet one, not
		// in this one.
		const quiet = createVoiceActivityState();
		expect(stepVoiceActivity(quiet, 0.02, 0, cfg).action).toBe('start');
		expect(stepVoiceActivity(noisy, 0.02, 0, cfg).action).toBe('idle');
	});
});

describe('an utterance', () => {
	it('starts on the first frame above the threshold', () => {
		const { state, actions } = feed([
			...frames(ROOM_RMS, 10),
			{ rms: SPEECH_RMS, at: 160 }
		]);
		expect(actions.at(-1)).toBe('start');
		expect(state.speechStartedAt).toBe(160);
		expect(state.lastSoundAt).toBe(160);
	});

	it('keeps listening while speech continues', () => {
		const { actions } = feed(frames(SPEECH_RMS, 40));
		expect(actions[0]).toBe('start');
		expect(new Set(actions.slice(1))).toEqual(new Set(['listening']));
	});

	it('transcribes once the configured silence has elapsed', () => {
		// One second of speech, then silence.
		const { state, actions } = feed([
			...frames(SPEECH_RMS, 63),
			...frames(ROOM_RMS, 200, 1008)
		]);
		expect(actions).toContain('transcribe');
		expect(actions.filter((a) => a === 'transcribe')).toHaveLength(1);
		// Reset, so the next utterance starts clean, but the learned floor
		// survives: recalibrating from zero on every turn throws away exactly
		// the measurement that keeps the room from being heard as speech.
		expect(state.speechStartedAt).toBeNull();
		expect(state.lastSoundAt).toBeNull();
	});

	it('does not transcribe before the silence has elapsed', () => {
		const { actions } = feed([
			...frames(SPEECH_RMS, 63),
			...frames(ROOM_RMS, 60, 1008)
		]);
		expect(actions).not.toContain('transcribe');
	});

	it('discards a click too short to be a sentence', () => {
		// A door, a keyboard, one frame loud, then quiet. Transcribing this is
		// what floods the transcription endpoint and feeds the model whatever
		// the recogniser hallucinates out of noise.
		const { actions } = feed([
			{ rms: SPEECH_RMS, at: 0 },
			...frames(ROOM_RMS, 200, 16)
		]);
		expect(actions).toContain('discard');
		expect(actions).not.toContain('transcribe');
	});

	it('keeps a word as short as yes', () => {
		// The other side of the discard rule, and the one that matters more in
		// a conversation: "yes", "no" and "stop" are around three hundred
		// milliseconds of voiced audio. A minimum long enough to be safe from
		// door slams and short enough to keep these is the whole point of
		// picking the number, and a threshold that silently swallows "yes" is
		// worse than one that occasionally transcribes a cough.
		const { actions } = feed([
			...frames(SPEECH_RMS, 19),
			...frames(ROOM_RMS, 200, 304)
		]);
		expect(actions).toContain('transcribe');
		expect(actions).not.toContain('discard');
	});

	it('stops at the hard cap even if the noise never stops', () => {
		// The guarantee the issue asks for: the microphone cannot stay open
		// forever, whatever the room does. Continuous loud noise, no gap.
		const total = Math.ceil(cfg.maxUtteranceMs / 16) + 10;
		const { actions } = feed(frames(SPEECH_RMS, total));
		expect(actions).toContain('transcribe');
		const stopIndex = actions.indexOf('transcribe');
		expect(stopIndex * 16).toBeLessThanOrEqual(cfg.maxUtteranceMs + 16);
		// And the state is reset behind it, so the next frame opens a fresh
		// utterance rather than tripping the cap again immediately.
		expect(actions[stopIndex + 1]).toBe('start');
	});

	it('runs a second utterance after the first', () => {
		const { actions } = feed([
			...frames(SPEECH_RMS, 63),
			...frames(ROOM_RMS, 200, 1008),
			...frames(SPEECH_RMS, 63, 4300),
			...frames(ROOM_RMS, 200, 5308)
		]);
		expect(actions.filter((a) => a === 'transcribe')).toHaveLength(2);
		expect(actions.filter((a) => a === 'start')).toHaveLength(2);
	});
});

describe('the reducer itself', () => {
	it('never mutates the state it is given', () => {
		const state = createVoiceActivityState();
		const frozen = Object.freeze({ ...state });
		const next = stepVoiceActivity(frozen, SPEECH_RMS, 0, cfg);
		expect(frozen.speechStartedAt).toBeNull();
		expect(next.state).not.toBe(frozen);
	});

	it('honours an overridden config', () => {
		const impatient: VoiceActivityConfig = { ...cfg, silenceMs: 200, minUtteranceMs: 0 };
		const { actions } = feed(
			[...frames(SPEECH_RMS, 20), ...frames(ROOM_RMS, 40, 320)],
			impatient
		);
		expect(actions).toContain('transcribe');
	});
});

describe('the call overlay uses it (issue #1627)', () => {
	const overlay = readFileSync(
		fileURLToPath(new URL('../components/chat/MessageInput/CallOverlay.svelte', import.meta.url)),
		'utf8'
	);

	it('imports the shared decision instead of rolling its own', () => {
		expect(overlay).toContain('voiceActivity');
		expect(overlay).toContain('stepVoiceActivity');
	});

	it('closes the analyser context it opens', () => {
		// One AudioContext is created per utterance cycle. Nothing closed it,
		// which was unreachable while the loop never exited and is reachable now
		// that it does: a browser caps how many contexts a document may hold, so
		// leaking one per utterance trades a microphone that never stops for a
		// microphone that stops a handful of times and then never again.
		expect(overlay).toContain('audioContext.close()');
	});

	it('binds the frame loop to the recorder it started with', () => {
		// toggleMute stops the recorder directly, which recycles it through
		// stopRecordingCallback and starts a second loop. A guard that only
		// asked whether some recorder existed left the first loop running too,
		// so two loops drove one recorder with two sets of state.
		expect(overlay).toContain('mediaRecorder !== recorder');
	});

	it('drives the decision from a monotonic clock', () => {
		// The wall clock can move backwards, and both the silence timeout and
		// the hard cap are differences against whatever clock is passed in.
		expect(overlay).toContain('rmsLevel, performance.now()');
		expect(overlay).not.toContain('rmsLevel, Date.now()');
	});

	it('no longer treats any audible bin as speech', () => {
		// The frequency data is what the old any-bin trigger read, and nothing
		// else in the overlay ever wanted it. Pinning the analyser call rather
		// than the expression means a differently-worded copy of the same
		// mistake fails here too, and it survives the comment above the fix
		// quoting the expression verbatim.
		expect(overlay).not.toContain('getByteFrequencyData');
	});
});
