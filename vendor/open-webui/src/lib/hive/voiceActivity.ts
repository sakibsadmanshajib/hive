// Voice activity detection for the chat call overlay (issue #1627).
//
// The overlay used to decide that someone was speaking with
// `domainData.some((value) => value > 0)`, which is true as soon as any single
// frequency bin clears the analyser's -55 dBFS floor. That is not a speech
// threshold, it is an "is the microphone on" check, and in an ordinary room it
// held on nearly every animation frame. The silence timer was therefore reset
// forever, `mediaRecorder.stop()` was never reached, and the microphone stayed
// open for as long as the overlay did.
//
// The replacement measures loudness rather than presence: the RMS of the time
// domain data, which the overlay already computes for its visualiser, against
// a threshold that tracks the room's own noise floor. It lives outside the
// component so it can be exercised frame by frame in a unit test; the overlay
// keeps only the wiring.

export interface VoiceActivityConfig {
	/** Below this RMS nothing is ever treated as speech, however quiet the room. */
	minRms: number;
	/** Speech has to clear the tracked noise floor by this factor. */
	floorMultiplier: number;
	/** Weight of the running noise floor estimate, per frame, while idle. */
	floorSmoothing: number;
	/** Quiet time after speech that ends an utterance. */
	silenceMs: number;
	/** Shorter than this is a door or a keystroke, not a sentence. */
	minUtteranceMs: number;
	/** Hard cap. The microphone cannot stay open longer than this, ever. */
	maxUtteranceMs: number;
}

// ponytail: these six numbers are the calibration knobs, and a room is not a
// spreadsheet. minRms is set for a browser capture with automatic gain
// control on, where speech lands around 0.05 to 0.2 RMS and a quiet room sits
// under 0.01. Raise minRms if the overlay starts on room noise, lower it if a
// soft speaker is not heard. Everything else is deliberately fixed: there is
// no settings surface for any of this, so an override here is the only knob,
// and a knob nothing writes to would just be a lie in the settings panel.
export const DEFAULT_VOICE_ACTIVITY_CONFIG: VoiceActivityConfig = Object.freeze({
	minRms: 0.015,
	floorMultiplier: 2.5,
	floorSmoothing: 0.98,
	silenceMs: 2000,
	minUtteranceMs: 400,
	maxUtteranceMs: 30000
});

export interface VoiceActivityState {
	/** Running estimate of the room, updated only while nobody is speaking. */
	noiseFloor: number;
	/** When the current utterance began, or null when idle. */
	speechStartedAt: number | null;
	/** When this utterance was last above the threshold. */
	lastSoundAt: number | null;
}

export type VoiceActivityAction =
	/** Nothing is being said. */
	| 'idle'
	/** An utterance just began: start the recorder. */
	| 'start'
	/** An utterance is in progress. */
	| 'listening'
	/** The utterance ended: stop the recorder and send what was captured. */
	| 'transcribe'
	/** Too short to be speech: stop the recorder and throw the audio away. */
	| 'discard';

export interface VoiceActivityStep {
	state: VoiceActivityState;
	action: VoiceActivityAction;
}

export const createVoiceActivityState = (): VoiceActivityState => ({
	noiseFloor: 0,
	speechStartedAt: null,
	lastSoundAt: null
});

/**
 * The level a frame has to beat to count as speech: never below `minRms`, and
 * lifted above a noisy room by the tracked floor.
 */
export const speechThreshold = (
	state: VoiceActivityState,
	config: VoiceActivityConfig = DEFAULT_VOICE_ACTIVITY_CONFIG
): number => Math.max(config.minRms, state.noiseFloor * config.floorMultiplier);

/**
 * One analyser frame in, one decision out. Pure: the caller holds the state.
 */
export const stepVoiceActivity = (
	state: VoiceActivityState,
	rms: number,
	now: number,
	config: VoiceActivityConfig = DEFAULT_VOICE_ACTIVITY_CONFIG
): VoiceActivityStep => {
	// A broken or absent reading is silence, not speech. An analyser that
	// hands back NaN must not be able to open the microphone, and must not be
	// allowed to poison the noise floor either.
	const level = Number.isFinite(rms) && rms > 0 ? rms : 0;
	const loud = level > speechThreshold(state, config);

	if (state.speechStartedAt === null || state.lastSoundAt === null) {
		if (loud) {
			return {
				state: { ...state, speechStartedAt: now, lastSoundAt: now },
				action: 'start'
			};
		}

		// Learn the room. Clamped at minRms so the estimate cannot chase its
		// own threshold upwards until the overlay is deaf to the user.
		const smoothed =
			state.noiseFloor * config.floorSmoothing + level * (1 - config.floorSmoothing);

		return {
			state: { ...state, noiseFloor: Math.min(config.minRms, smoothed) },
			action: 'idle'
		};
	}

	const lastSoundAt = loud ? now : state.lastSoundAt;
	const idle: VoiceActivityState = {
		noiseFloor: state.noiseFloor,
		speechStartedAt: null,
		lastSoundAt: null
	};

	// The guarantee the issue asks for. Whatever the room is doing, the
	// recorder stops here, so an open microphone is always bounded.
	if (now - state.speechStartedAt >= config.maxUtteranceMs) {
		return { state: idle, action: 'transcribe' };
	}

	if (now - lastSoundAt >= config.silenceMs) {
		const spoken = lastSoundAt - state.speechStartedAt;
		return { state: idle, action: spoken >= config.minUtteranceMs ? 'transcribe' : 'discard' };
	}

	return { state: { ...state, lastSoundAt }, action: 'listening' };
};
