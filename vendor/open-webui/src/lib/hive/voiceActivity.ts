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
	/** Ceiling on the tracked floor, so the estimate cannot run away. */
	maxNoiseFloor: number;
	/** Time spent listening to the room before any utterance may begin. */
	calibrationMs: number;
	/** Quiet time after speech that ends an utterance. */
	silenceMs: number;
	/** Shorter than this is a door or a keystroke, not a word. */
	minUtteranceMs: number;
	/** Hard cap. The microphone cannot stay open longer than this, ever. */
	maxUtteranceMs: number;
}

// ponytail: these numbers are the calibration knobs, and a room is not a
// spreadsheet. minRms is set for a browser capture with automatic gain
// control on, where speech lands around 0.05 to 0.2 RMS and a quiet room sits
// under 0.01. Raise minRms if the overlay starts on room noise, lower it if a
// soft speaker is not heard. There is no settings surface for any of this, so
// an override here is the only knob, and a knob nothing writes to would just
// be a lie in the settings panel.
export const DEFAULT_VOICE_ACTIVITY_CONFIG: VoiceActivityConfig = Object.freeze({
	minRms: 0.015,
	floorMultiplier: 2.5,
	floorSmoothing: 0.98,
	// A room this loud drowns quiet speech anyway, so tracking past it buys
	// nothing and costs the ability to hear anyone at all.
	maxNoiseFloor: 0.05,
	// Half a second of listening before the first utterance may begin. Without
	// it a call opened into an already-loud room has no quiet frame to learn
	// from, so the first frame starts an utterance, the hard cap ends it, the
	// noise is transcribed, and it repeats: the reported flood, rebuilt.
	calibrationMs: 500,
	silenceMs: 2000,
	// Long enough to drop a door or a keystroke, which are tens of
	// milliseconds of audio, and short enough to keep "yes", "no" and "stop",
	// which are around three hundred. Erring high here would silently swallow
	// the shortest answers a person gives out loud, which is a worse failure
	// than transcribing the occasional cough.
	minUtteranceMs: 250,
	maxUtteranceMs: 30000
});

export interface VoiceActivityState {
	/** Running estimate of the room, updated only while nobody is speaking. */
	noiseFloor: number;
	/** When this state first saw a frame, which is when calibration began. */
	listeningSince: number | null;
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
	listeningSince: null,
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
	const listeningSince = state.listeningSince ?? now;

	if (state.speechStartedAt === null || state.lastSoundAt === null) {
		// Nobody is speaking, so this frame is the room. Every frame counts,
		// loud ones included: a call that opens into a noisy room only ever
		// sees loud frames, and refusing to learn from them is what made the
		// first frame look like speech.
		const smoothed =
			state.noiseFloor * config.floorSmoothing + level * (1 - config.floorSmoothing);
		const idle: VoiceActivityState = {
			...state,
			listeningSince,
			noiseFloor: Math.min(config.maxNoiseFloor, smoothed)
		};

		// The threshold is only trustworthy once the room has been measured.
		if (loud && now - listeningSince >= config.calibrationMs) {
			return {
				state: { ...state, listeningSince, speechStartedAt: now, lastSoundAt: now },
				action: 'start'
			};
		}

		return { state: idle, action: 'idle' };
	}

	const lastSoundAt = loud ? now : state.lastSoundAt;
	const idle: VoiceActivityState = {
		noiseFloor: state.noiseFloor,
		listeningSince,
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

	return { state: { ...state, listeningSince, lastSoundAt }, action: 'listening' };
};
