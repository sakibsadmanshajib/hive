import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import {
	DEFAULT_VOICE_ACTIVITY_CONFIG,
	createVoiceActivityState,
	parkVoiceActivity,
	shouldSuppressCapture,
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

// Nothing may start until the room has been measured, so every utterance test
// opens with the room. Four frames past the window, so the boundary itself is
// never what a timing assertion turns on.
const OPENING_FRAMES = Math.ceil(cfg.calibrationMs / 16) + 4;
const SPEECH_AT = OPENING_FRAMES * 16;
const opening = () => frames(ROOM_RMS, OPENING_FRAMES);

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

	it('never starts in a room that was already loud when the call opened', () => {
		// Raised by the CodeRabbit review of this change, and real. A room at
		// 0.02 RMS is over the absolute minimum and under anything anyone would
		// call speech, and if the overlay opens into it there is no quiet frame
		// to learn from: the first frame started an utterance, the hard cap
		// ended it thirty seconds later, the noise was transcribed, and the
		// whole thing repeated forever. That is the reported flood, rebuilt.
		const { actions } = feed(frames(0.02, 3750));
		expect(new Set(actions)).toEqual(new Set(['idle']));
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
		expect(noisy.noiseFloor).toBeLessThanOrEqual(cfg.maxNoiseFloor);
		expect(speechThreshold(noisy, cfg)).toBeLessThanOrEqual(
			cfg.maxNoiseFloor * cfg.floorMultiplier
		);

		// Same level, two rooms, both past calibration: loud enough to be
		// speech in the quiet one, not in this one.
		const quiet = feed(frames(0, OPENING_FRAMES)).state;
		expect(stepVoiceActivity(quiet, 0.02, 10000, cfg).action).toBe('start');
		expect(stepVoiceActivity(noisy, 0.02, 10000, cfg).action).toBe('idle');
	});

	it('will not start inside the calibration window, speech or not', () => {
		// Half a second of the room is the price of knowing what the room is.
		// It is paid once per recorder, and the same speech one frame later is
		// heard normally.
		const early = stepVoiceActivity(createVoiceActivityState(), SPEECH_RMS, 0, cfg);
		expect(early.action).toBe('idle');
		expect(early.state.listeningSince).toBe(0);

		const late = stepVoiceActivity(early.state, SPEECH_RMS, cfg.calibrationMs, cfg);
		expect(late.action).toBe('start');
	});
});

describe('an utterance', () => {
	it('starts on the first frame above the threshold', () => {
		const { state, actions } = feed([...opening(), { rms: SPEECH_RMS, at: SPEECH_AT }]);
		expect(actions.at(-1)).toBe('start');
		expect(state.speechStartedAt).toBe(SPEECH_AT);
		expect(state.lastSoundAt).toBe(SPEECH_AT);
	});

	it('keeps listening while speech continues', () => {
		const { actions } = feed([...opening(), ...frames(SPEECH_RMS, 40, SPEECH_AT)]);
		expect(actions[OPENING_FRAMES]).toBe('start');
		expect(new Set(actions.slice(OPENING_FRAMES + 1))).toEqual(new Set(['listening']));
	});

	it('transcribes once the configured silence has elapsed', () => {
		// One second of speech, then silence.
		const { state, actions } = feed([
			...opening(),
			...frames(SPEECH_RMS, 63, SPEECH_AT),
			...frames(ROOM_RMS, 200, SPEECH_AT + 1008)
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
			...opening(),
			...frames(SPEECH_RMS, 63, SPEECH_AT),
			...frames(ROOM_RMS, 60, SPEECH_AT + 1008)
		]);
		// The utterance really did start, so the absence below is the silence
		// timer not having elapsed rather than nothing having happened.
		expect(actions).toContain('start');
		expect(actions).not.toContain('transcribe');
	});

	it('discards a click too short to be a sentence', () => {
		// A door, a keyboard, one frame loud, then quiet. Transcribing this is
		// what floods the transcription endpoint and feeds the model whatever
		// the recogniser hallucinates out of noise.
		const { actions } = feed([
			...opening(),
			{ rms: SPEECH_RMS, at: SPEECH_AT },
			...frames(ROOM_RMS, 200, SPEECH_AT + 16)
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
			...opening(),
			...frames(SPEECH_RMS, 19, SPEECH_AT),
			...frames(ROOM_RMS, 200, SPEECH_AT + 304)
		]);
		expect(actions).toContain('transcribe');
		expect(actions).not.toContain('discard');
	});

	it('stops at the hard cap even if the noise never stops', () => {
		// The guarantee the issue asks for: the microphone cannot stay open
		// forever, whatever the room does. Continuous loud noise, no gap.
		const total = Math.ceil(cfg.maxUtteranceMs / 16) + 10;
		const { actions } = feed([...opening(), ...frames(SPEECH_RMS, total, SPEECH_AT)]);
		expect(actions).toContain('transcribe');
		const stopIndex = actions.indexOf('transcribe');
		expect((stopIndex - OPENING_FRAMES) * 16).toBeLessThanOrEqual(cfg.maxUtteranceMs + 16);
		// And the state is reset behind it, so the next frame opens a fresh
		// utterance rather than tripping the cap again immediately.
		expect(actions[stopIndex + 1]).toBe('start');
	});

	it('runs a second utterance after the first', () => {
		const { actions } = feed([
			...opening(),
			...frames(SPEECH_RMS, 63, SPEECH_AT),
			...frames(ROOM_RMS, 200, SPEECH_AT + 1008),
			...frames(SPEECH_RMS, 63, SPEECH_AT + 4300),
			...frames(ROOM_RMS, 200, SPEECH_AT + 5308)
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
		const impatient: VoiceActivityConfig = {
			...cfg,
			calibrationMs: 0,
			silenceMs: 200,
			minUtteranceMs: 0
		};
		const { actions } = feed(
			[...frames(SPEECH_RMS, 20), ...frames(ROOM_RMS, 40, 320)],
			impatient
		);
		expect(actions[0]).toBe('start');
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

// The second half of issue #1627, which PR #1662 did not reach: the flood.
//
// PR #1662 gave the overlay a real speech threshold, so room noise no longer
// opens the microphone. What it left in place is the amplifier that turns
// ordinary speech into stacked requests. `stopRecordingCallback` restarts
// capture BEFORE the utterance it just captured has been transcribed, and the
// only suppressions were mute and a playing voice. `assistantSpeaking` rises
// at `chat:start`, so the model turn was already covered; the transcription
// round trip before it was not. That is a network call to a speech to text
// provider, and anything said during it opened a second upload against the
// same account, then a third, until the provider answered 429.
describe('capture suppression while a turn is in flight (issue #1627)', () => {
	const base = {
		muted: false,
		assistantSpeaking: false,
		turnInFlight: false,
		voiceInterruption: false
	};

	it('lets an idle overlay hear the room', () => {
		expect(shouldSuppressCapture(base)).toBe(false);
	});

	it('suppresses while the utterance just captured is still being transcribed', () => {
		// The fix. Without it a second utterance opens a second concurrent
		// upload to the same provider account while the first is still going.
		expect(shouldSuppressCapture({ ...base, turnInFlight: true })).toBe(true);
	});

	it('still suppresses while the assistant is speaking', () => {
		// Unchanged behaviour, pinned so the new condition cannot be written in
		// a way that drops the old one.
		expect(shouldSuppressCapture({ ...base, assistantSpeaking: true })).toBe(true);
	});

	it('lets voice interruption talk over the assistant', () => {
		expect(
			shouldSuppressCapture({ ...base, assistantSpeaking: true, voiceInterruption: true })
		).toBe(false);
	});

	it('does not let voice interruption reopen the transcription window', () => {
		// The setting exists so a person can talk over the assistant. During
		// the transcription round trip no reply has begun, so there is nothing
		// to talk over: honouring the setting there would buy no interruption
		// and would cost exactly the concurrent upload this fix prevents.
		expect(
			shouldSuppressCapture({ ...base, turnInFlight: true, voiceInterruption: true })
		).toBe(true);
	});

	it('leaves barge-in during generation exactly as it was', () => {
		// The regression this flag's narrowness exists to avoid. Once the
		// upload has finished the assistant is answering, so `assistantSpeaking`
		// governs and "Allow Voice Interruption in Call" still works. A flag
		// that spanned the whole turn would return true here and silently
		// disable that setting.
		expect(
			shouldSuppressCapture({
				...base,
				assistantSpeaking: true,
				turnInFlight: false,
				voiceInterruption: true
			})
		).toBe(false);
		// And with the setting off it still suppresses, unchanged.
		expect(
			shouldSuppressCapture({ ...base, assistantSpeaking: true, turnInFlight: false })
		).toBe(true);
	});

	it('keeps mute above everything', () => {
		// Mute is the user holding the microphone shut. Nothing overrides it.
		expect(
			shouldSuppressCapture({ ...base, muted: true, voiceInterruption: true })
		).toBe(true);
		expect(shouldSuppressCapture({ ...base, muted: true, assistantSpeaking: true })).toBe(true);
	});
});

describe('parking the detector while suppressed (issue #1627)', () => {
	it('keeps the learned noise floor, because the room has not changed', () => {
		const parked = parkVoiceActivity({
			noiseFloor: 0.02,
			listeningSince: 1000,
			speechStartedAt: 1200,
			lastSoundAt: 1400
		});
		expect(parked.noiseFloor).toBe(0.02);
	});

	it('drops the utterance and re-arms calibration', () => {
		const parked = parkVoiceActivity({
			noiseFloor: 0.02,
			listeningSince: 1000,
			speechStartedAt: 1200,
			lastSoundAt: 1400
		});
		expect(parked.speechStartedAt).toBeNull();
		expect(parked.lastSoundAt).toBeNull();
		// The one that matters. Leaving `listeningSince` set means the
		// calibration window has long expired when capture resumes, so the very
		// first frame after a reply can open an utterance without the room ever
		// having been measured.
		expect(parked.listeningSince).toBeNull();
	});

	it('is what a suppressed frame must do instead of stepping with a zero', () => {
		// The concrete reason parking exists, measured rather than asserted in
		// prose: stepping a suppressed frame as silence smooths the tracked
		// floor towards zero, and a reply lasts hundreds of frames.
		let stepped = { noiseFloor: 0.02, listeningSince: 0, speechStartedAt: null, lastSoundAt: null };
		for (let frame = 1; frame <= 300; frame += 1) {
			stepped = stepVoiceActivity(stepped, 0, frame * 16).state;
		}
		expect(stepped.noiseFloor).toBeLessThan(0.002);
		expect(speechThreshold(stepped)).toBe(DEFAULT_VOICE_ACTIVITY_CONFIG.minRms);

		const parked = parkVoiceActivity({
			noiseFloor: 0.02,
			listeningSince: 0,
			speechStartedAt: null,
			lastSoundAt: null
		});
		expect(speechThreshold(parked)).toBeGreaterThan(DEFAULT_VOICE_ACTIVITY_CONFIG.minRms);
	});
});

describe('the call overlay wires the suppression and always clears the turn (issue #1627)', () => {
	const overlay = readFileSync(
		fileURLToPath(new URL('../components/chat/MessageInput/CallOverlay.svelte', import.meta.url)),
		'utf8'
	);

	/**
	 * The body of `stopRecordingCallback`, so an assertion about it cannot be
	 * satisfied by an unrelated construct elsewhere in a 1100 line component.
	 */
	const stopRecordingCallback = (() => {
		const start = overlay.indexOf('const stopRecordingCallback');
		const end = overlay.indexOf('const startRecording');
		expect(start).toBeGreaterThan(-1);
		expect(end).toBeGreaterThan(start);
		return overlay.slice(start, end);
	})();

	it('asks the shared decision rather than inlining the condition', () => {
		expect(overlay).toContain('shouldSuppressCapture');
	});

	it('feeds it the transcription window and not the whole turn', () => {
		// `transcribing`, deliberately, not `loading`. `loading` stays true for
		// the entire turn including generation, and suppressing on that would
		// kill barge-in for anyone with voice interruption enabled, which is a
		// setting this fix has no business touching. The window that was open
		// is the upload, so that is the window the flag covers.
		expect(overlay).toContain('turnInFlight: transcribing');
		expect(overlay).not.toContain('turnInFlight: loading');
	});

	it('clears the transcription flag before the model turn begins', () => {
		// The boundary that keeps barge-in alive. If this were cleared after
		// submitPrompt instead, the flag would span generation and the
		// unconditional suppression above would silence the microphone for the
		// whole reply.
		const handler = overlay.slice(
			overlay.indexOf('const transcribeHandler'),
			overlay.indexOf('const stopRecordingCallback')
		);
		// Matched on the call, not the word: a comment mentioning submitPrompt
		// sits inside the finally block and would otherwise be found first,
		// which made this assertion fail for a reason that was not the code.
		const cleared = handler.indexOf('transcribing = false;');
		const submitted = handler.indexOf('await submitPrompt(');
		expect(cleared).toBeGreaterThan(-1);
		expect(submitted).toBeGreaterThan(-1);
		expect(cleared).toBeLessThan(submitted);
	});

	it('parks the detector rather than stepping it with a forced zero', () => {
		expect(overlay).toContain('parkVoiceActivity(voiceActivity)');
	});

	it('clears the in-flight turn even when the turn throws', () => {
		// `submitPrompt` is awaited with no catch of its own. Before this, an
		// exception from it escaped stopRecordingCallback as an unhandled
		// rejection with `loading` still true, so the overlay showed its
		// loading state forever. That is fatal now rather than merely ugly:
		// capture is suppressed while `loading` holds, so a stuck flag would
		// mean a call that never hears anything again.
		//
		// Scoped to the function, not the file: `} finally {` anywhere in a
		// component this size would satisfy a whole-file search, so that
		// assertion could go green for a reason unrelated to this fix.
		expect(stopRecordingCallback).toContain('} finally {');
		expect(stopRecordingCallback).toContain('loading = false;');
	});
});
