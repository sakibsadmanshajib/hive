// Voice-mode capture for issue #1627.
//
// Drives the real chat front end in Chromium with a fake microphone that
// speaks for two seconds and then goes quiet, and records when the recorder
// started and stopped, what the overlay said it was doing, and every request
// the stub upstream received.
//
// The page instrumentation only observes: it wraps MediaRecorder.start/stop
// and getUserMedia to timestamp them. It changes no behaviour, and the same
// script runs against both builds.

import { chromium } from 'playwright';
import { mkdirSync, writeFileSync, appendFileSync } from 'node:fs';

const BASE = process.env.OWUI_URL ?? 'http://proof1627-owui:8080';
const OUT = process.env.OUT_DIR ?? '/out';
const LABEL = process.env.LABEL ?? 'after';
const WAV = process.env.WAV ?? '/work/speech.wav';

const EMAIL = `voice-proof-${LABEL}@hive.invalid`;
// Supplied by the caller, with no default. The account is throwaway and the
// container it lives in is deleted after the capture, but a literal in a
// public repository is a literal in a public repository, and the next person
// to copy this file may point it somewhere that matters.
const PASSWORD = process.env.PROOF_PASSWORD;
if (!PASSWORD) {
	throw new Error('PROOF_PASSWORD is required, see docs/proof/voice-listen-stop-1627/README.md');
}

mkdirSync(OUT, { recursive: true });
const logPath = `${OUT}/capture-${LABEL}.log`;
writeFileSync(logPath, '');

const t0 = Date.now();
const say = (line) => {
	const stamped = `[${((Date.now() - t0) / 1000).toFixed(3).padStart(8)}s] ${line}`;
	console.log(stamped);
	appendFileSync(logPath, stamped + '\n');
};

// Chromium's own fake capture device (--use-fake-device-for-media-capture)
// enumerates zero audio devices inside this container, so the microphone is
// synthesised in the page instead, with Web Audio. Everything downstream of
// getUserMedia is the real thing: a real MediaStream, the real analyser, the
// real MediaRecorder, the real component. What is synthetic is the room, and
// it is synthetic in a way that reproduces the reported conditions exactly:
//
//   * a constant low noise floor for the whole session, which is what an
//     ordinary room has and what the old any-bin trigger heard as speech, and
//   * one two-second burst of voiced signal, starting one second in.
const probe = () => {
	window.__voiceProbe = { events: [], tracks: [] };

	const fakeMicrophone = () => {
		const ctx = new AudioContext();
		const destination = ctx.createMediaStreamDestination();

		// The room, and the shape of it matters. Flat white noise is not what a
		// room sounds like: real room noise is dominated by low frequency
		// rumble, a fan or an air conditioner or mains hum, and that is what
		// puts individual analyser bins above the -55 dBFS floor the old
		// trigger tested. Spread the same total energy evenly across a
		// thousand bins instead and every bin reads zero, which would let the
		// old code look like it worked.
		//
		// So: two low tones plus a hiss, about -40 dBFS combined. Quiet enough
		// that no detector should call it speech, ordinary enough that a laptop
		// in a room with a fan produces it.
		for (const [frequency, amplitude] of [[90, 0.012], [123, 0.006]]) {
			const hum = ctx.createOscillator();
			hum.frequency.value = frequency;
			const humGain = ctx.createGain();
			humGain.gain.value = amplitude;
			hum.connect(humGain).connect(destination);
			hum.start();
		}

		const noiseBuffer = ctx.createBuffer(1, ctx.sampleRate * 2, ctx.sampleRate);
		const noise = noiseBuffer.getChannelData(0);
		for (let i = 0; i < noise.length; i++) {
			noise[i] = Math.random() * 2 - 1;
		}
		const room = ctx.createBufferSource();
		room.buffer = noiseBuffer;
		room.loop = true;
		const roomGain = ctx.createGain();
		roomGain.gain.value = 0.008;
		room.connect(roomGain).connect(destination);
		room.start();

		// The speaker: a harmonic stack with a syllabic tremolo, one second in,
		// for two seconds, then silent for the rest of the session.
		const voice = ctx.createOscillator();
		voice.type = 'sawtooth';
		voice.frequency.value = 150;

		// Syllables. The tremolo modulates its own stage, never the envelope
		// below it: a gain AudioParam sums its intrinsic value with whatever is
		// connected to it, so modulating the envelope directly would make the
		// "speaker" audible before the envelope ever opens.
		const syllables = ctx.createGain();
		syllables.gain.value = 0.6;
		const tremolo = ctx.createOscillator();
		tremolo.frequency.value = 4;
		const tremoloDepth = ctx.createGain();
		tremoloDepth.gain.value = 0.4;
		tremolo.connect(tremoloDepth).connect(syllables.gain);

		const envelope = ctx.createGain();
		envelope.gain.value = 0;
		voice.connect(syllables).connect(envelope).connect(destination);
		voice.start();
		tremolo.start();

		const now = ctx.currentTime;
		envelope.gain.setValueAtTime(0, now + 1.0);
		envelope.gain.linearRampToValueAtTime(0.5, now + 1.05);
		envelope.gain.setValueAtTime(0.5, now + 2.95);
		envelope.gain.linearRampToValueAtTime(0, now + 3.0);

		ctx.resume?.();
		window.__voiceProbe.events.push({
			name: 'fake microphone: noise floor throughout, speech from +1.0s to +3.0s',
			at: Math.round(performance.now())
		});
		return destination.stream;
	};

	let fakeStream = null;
	const mark = (name, extra = {}) =>
		window.__voiceProbe.events.push({ name, at: Math.round(performance.now()), ...extra });

	const originalStart = MediaRecorder.prototype.start;
	MediaRecorder.prototype.start = function (...args) {
		mark('MediaRecorder.start');
		return originalStart.apply(this, args);
	};

	const originalStop = MediaRecorder.prototype.stop;
	MediaRecorder.prototype.stop = function (...args) {
		mark('MediaRecorder.stop', { state: this.state });
		return originalStop.apply(this, args);
	};

	window.__liveMics = () =>
		window.__voiceProbe.tracks.filter((t) => t.readyState === 'live').length;

	navigator.mediaDevices.getUserMedia = async (constraints) => {
		if (!constraints?.audio) {
			throw new Error('this capture only fakes audio');
		}
		// A fresh stream per call on purpose: the composer's voice-mode button
		// asks for the microphone once purely to check the permission and stops
		// those tracks immediately, so the overlay's own request has to get a
		// live one. Timing is therefore relative to the overlay opening.
		mark('getUserMedia(audio)');
		fakeStream = fakeMicrophone();
		window.__voiceProbe.tracks.push(...fakeStream.getAudioTracks());
		return fakeStream;
	};
};

const overlayState = async (page) => {
	for (const label of ['Listening...', 'Thinking...', 'Tap to interrupt', 'Muted']) {
		if (await page.getByText(label, { exact: true }).first().isVisible().catch(() => false)) {
			return label;
		}
	}
	return '(no state label visible)';
};

const run = async () => {
	const browser = await chromium.launch({
		args: [
			'--use-fake-device-for-media-capture',
			'--use-fake-ui-for-media-stream',
			`--use-file-for-fake-audio-capture=${WAV}%noloop`,
			'--autoplay-policy=no-user-gesture-required'
		]
	});

	// The account is created through the API rather than by driving the sign
	// up form: the form is not what this capture is about, and the fork's auth
	// page does not lay out reliably at this viewport in headless Chromium.
	const signup = await fetch(`${BASE}/api/v1/auths/signup`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ name: 'Voice Proof', email: EMAIL, password: PASSWORD })
	});
	let token = null;
	if (signup.ok) {
		token = (await signup.json()).token;
		say('created the local proof account through the API');
	} else {
		const signin = await fetch(`${BASE}/api/v1/auths/signin`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ email: EMAIL, password: PASSWORD })
		});
		if (!signin.ok) {
			// Reading a token out of an error body is how a capture ends up
			// reporting a browser failure for what was an auth failure.
			throw new Error(`signin failed: ${signin.status} ${signin.statusText}`);
		}
		token = (await signin.json()).token;
		say('signed the existing local proof account in through the API');
	}
	if (!token) {
		throw new Error('no session token');
	}

	const context = await browser.newContext({
		permissions: ['microphone'],
		viewport: { width: 1280, height: 900 },
		recordVideo: { dir: `${OUT}/video-${LABEL}`, size: { width: 1280, height: 900 } }
	});
	const origin = new URL(BASE);
	await context.addCookies([
		{ name: 'token', value: token, domain: origin.hostname, path: '/' }
	]);
	await context.addInitScript((value) => {
		try {
			localStorage.setItem('token', value);
		} catch (error) {
			/* first navigation may be about:blank */
		}
	}, token);
	await context.addInitScript(probe);

	const page = await context.newPage();
	page.on('console', (msg) => {
		const text = msg.text();
		if (/Recording|recorder|Sound|Silence|voice|error/i.test(text)) {
			say(`console: ${text.slice(0, 200)}`);
		}
	});

	say(`build under test: ${LABEL}`);
	say(`front end: ${BASE}`);

	await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' });
	await page.waitForTimeout(8000);
	say(`signed in, url=${page.url()}`);
	await page.screenshot({ path: `${OUT}/${LABEL}-00-signed-in.png` });

	const callButton = page.getByLabel('Voice mode').first();
	await callButton.waitFor({ state: 'visible', timeout: 30000 });
	say('voice mode button visible, opening the overlay');
	await callButton.click();
	await page.waitForTimeout(500);

	const opened = Date.now();
	const timeline = [];
	// The fake microphone speaks from 1s to 3s, so a working detector stops the
	// recorder about two seconds after that. The window runs well past it, to
	// show what the microphone does for the rest of the call.
	const polls = Number(process.env.POLLS ?? 32);
	const shotAt = new Set([1, 4, 9, 15, 19, 21, 23, 31, Math.floor(polls / 2), polls - 1]);
	for (let i = 0; i < polls; i++) {
		const at = ((Date.now() - opened) / 1000).toFixed(2);
		const state = await overlayState(page);
		const live = await page.evaluate(() => window.__liveMics?.() ?? -1);
		timeline.push({ at, state, live });
		if (shotAt.has(i)) {
			const shot = `${OUT}/${LABEL}-${String(i).padStart(2, '0')}-t${at}s.png`;
			await page.screenshot({ path: shot });
			say(`t+${at}s  overlay=${state}  liveMicTracks=${live}  -> ${shot.split('/').pop()}`);
		}
		await page.waitForTimeout(250);
	}

	const events = await page.evaluate(() => window.__voiceProbe?.events ?? []);
	const base = events.find((e) => e.name === 'getUserMedia(audio)')?.at ?? 0;
	say('recorder timeline, relative to the first getUserMedia:');
	for (const event of events) {
		say(`   +${((event.at - base) / 1000).toFixed(2)}s  ${event.name}${event.state ? ` (state=${event.state})` : ''}`);
	}

	const starts = events.filter((e) => e.name === 'MediaRecorder.start').length;
	const stops = events.filter((e) => e.name === 'MediaRecorder.stop').length;
	say(`recorder starts=${starts} stops=${stops}`);
	say(`overlay states seen: ${[...new Set(timeline.map((t) => t.state))].join(' | ')}`);

	writeFileSync(`${OUT}/timeline-${LABEL}.json`, JSON.stringify({ events, timeline }, null, 2));

	await page.screenshot({ path: `${OUT}/${LABEL}-99-final.png` });
	await browser.close();
	say('done');
};

run().catch(async (error) => {
	say(`FAILED: ${error.message}`);
	process.exitCode = 1;
});
