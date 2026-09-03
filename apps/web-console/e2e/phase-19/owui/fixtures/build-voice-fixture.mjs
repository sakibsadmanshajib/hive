// Builds the synthetic microphone track the voice capture speaks into
// (issue #1627).
//
// WHY A GENERATED FILE AND NOT A COMMITTED ONE
//
// Chrome's fake capture device reads a WAV from disk and presents it as a
// microphone, which is the only way a headless runner can "speak" at all. The
// file could have been committed, but a binary blob of speech is unreadable in
// review: nobody can tell from a diff how long the pauses are, and the pauses
// are the entire experiment. Generated from the phrase list below, the timing
// is in the source, in seconds, where a reviewer can check it against the
// silence threshold the fix uses.
//
// WHAT THE TRACK HAS TO CONTAIN, AND WHY
//
// The claim under proof is that a second utterance spoken while the first is
// still being transcribed and answered opens no second transcription. So the
// track is one opening utterance, then a gap longer than the two second
// silence threshold that ends it, then several short utterances spaced by gaps
// that would each close an utterance of their own.
//
// Before the fix every one of those later utterances opened its own concurrent
// transcription and its own model turn, because capture restarted immediately
// and only muting or a playing voice suppressed it. After the fix they land
// inside the in-flight turn and are suppressed, so the whole track produces
// exactly one transcription. That difference is the assertion, and it is why
// the later phrases are several short ones rather than one long one: a single
// long utterance would not close until it stopped, which could be after the
// turn had already settled, and would prove nothing either way.
//
// espeak-ng rather than a cloud voice: it is deterministic, offline, free, and
// its output is already the 16 bit PCM WAV the fake device requires. The words
// only have to be recognisable enough for speech to text to return something;
// the assertion that carries the claim is the request count, not the wording.

import { execFile } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

/**
 * The opening utterance. Long enough to clear the 250 ms minimum that drops a
 * door or a keystroke, and phrased as a question so the assistant answers with
 * something that takes a while to stream.
 */
export const OPENING_PHRASE = "Please count from one to forty, one number on each line.";

/**
 * The interruptions, spoken while the opening turn is still being transcribed
 * and answered. Each is short, and each is followed by a gap longer than the
 * two second silence threshold, so before the fix each one closed an utterance
 * of its own and opened its own transcription.
 */
export const INTERRUPTION_PHRASES = [
  "Are you still there.",
  "Hello can you hear me.",
  "I am still talking.",
];

/** Seconds of quiet after the opening phrase. Longer than silenceMs (2 s). */
const GAP_AFTER_OPENING_S = 2.6;
/** Seconds of quiet after each interruption. Also longer than silenceMs. */
const GAP_AFTER_INTERRUPTION_S = 2.4;
/**
 * Quiet tail. The fake device stops at end of file (the capture passes
 * %noloop), and this keeps the microphone hearing nothing rather than hearing
 * the file restart while the assistant is still speaking.
 */
const TAIL_S = 20;
/** Leading quiet, so the overlay's 500 ms calibration window measures silence. */
const LEAD_S = 1.5;

/**
 * Reads a RIFF/WAVE file into its format and its raw sample bytes.
 *
 * Chunk-walked rather than read at fixed offsets: espeak-ng emits a plain
 * canonical header today, but a WAV is a chunked format and a reader that
 * assumes data begins at byte 44 silently treats a LIST or fact chunk as
 * audio, which is noise at the front of the track rather than an error.
 */
function readWav(buf) {
  if (buf.toString("ascii", 0, 4) !== "RIFF" || buf.toString("ascii", 8, 12) !== "WAVE") {
    throw new Error("not a RIFF/WAVE file");
  }
  let fmt = null;
  let data = null;
  let off = 12;
  while (off + 8 <= buf.length) {
    const id = buf.toString("ascii", off, off + 4);
    const size = buf.readUInt32LE(off + 4);
    const body = buf.subarray(off + 8, off + 8 + size);
    if (id === "fmt ") {
      fmt = {
        audioFormat: body.readUInt16LE(0),
        channels: body.readUInt16LE(2),
        sampleRate: body.readUInt32LE(4),
        bitsPerSample: body.readUInt16LE(14),
      };
    } else if (id === "data") {
      data = body;
    }
    // Chunks are word aligned: an odd size carries one pad byte.
    off += 8 + size + (size % 2);
  }
  if (!fmt || !data) {
    throw new Error("WAV carries no fmt or no data chunk");
  }
  if (fmt.audioFormat !== 1 || fmt.bitsPerSample !== 16) {
    throw new Error(
      `WAV is not 16 bit PCM (format ${fmt.audioFormat}, ${fmt.bitsPerSample} bit); the fake capture device reads only that`,
    );
  }
  return { fmt, data };
}

/** A canonical 44 byte 16 bit PCM header wrapped around the given samples. */
function writeWav(fmt, data) {
  const blockAlign = (fmt.channels * fmt.bitsPerSample) / 8;
  const header = Buffer.alloc(44);
  header.write("RIFF", 0, "ascii");
  header.writeUInt32LE(36 + data.length, 4);
  header.write("WAVE", 8, "ascii");
  header.write("fmt ", 12, "ascii");
  header.writeUInt32LE(16, 16);
  header.writeUInt16LE(1, 20);
  header.writeUInt16LE(fmt.channels, 22);
  header.writeUInt32LE(fmt.sampleRate, 24);
  header.writeUInt32LE(fmt.sampleRate * blockAlign, 28);
  header.writeUInt16LE(blockAlign, 32);
  header.writeUInt16LE(fmt.bitsPerSample, 34);
  header.write("data", 36, "ascii");
  header.writeUInt32LE(data.length, 40);
  return Buffer.concat([header, data]);
}

/** Digital silence, which is what the noise floor estimate is meant to learn. */
function silence(fmt, seconds) {
  const blockAlign = (fmt.channels * fmt.bitsPerSample) / 8;
  return Buffer.alloc(Math.round(fmt.sampleRate * seconds) * blockAlign);
}

/**
 * One phrase through espeak-ng, returned as a parsed WAV.
 *
 * Awaited, never the sync form: this file sits inside the Playwright tree, and
 * a synchronous child there blocks the worker's event loop so the timeouts
 * around it never fire (tools/lint-no-sync-child-process-in-tests.mjs, and the
 * test recorded as passed at 30194 ms under a 30000 ms timeout that found it).
 */
async function speak(phrase, workDir, index) {
  const out = join(workDir, `phrase-${index}.wav`);
  await execFileAsync("espeak-ng", ["-w", out, "-s", "150", phrase]);
  return readWav(readFileSync(out));
}

/**
 * Writes the track to `outPath` and returns its timeline, which the capture
 * logs so the reader of a proof can see what was spoken and when.
 */
export async function buildVoiceFixture(outPath) {
  const workDir = mkdtempSync(join(tmpdir(), "hive-voice-fixture-"));
  try {
    const phrases = [OPENING_PHRASE, ...INTERRUPTION_PHRASES];
    const clips = [];
    for (const [i, phrase] of phrases.entries()) {
      clips.push(await speak(phrase, workDir, i));
    }
    const fmt = clips[0].fmt;
    for (const clip of clips) {
      if (clip.fmt.sampleRate !== fmt.sampleRate || clip.fmt.channels !== fmt.channels) {
        throw new Error("espeak-ng produced clips in more than one format");
      }
    }

    const blockAlign = (fmt.channels * fmt.bitsPerSample) / 8;
    const parts = [silence(fmt, LEAD_S)];
    const timeline = [{ at: 0, event: `silence ${LEAD_S}s (calibration)` }];
    let elapsed = LEAD_S;

    clips.forEach((clip, i) => {
      const seconds = clip.data.length / blockAlign / fmt.sampleRate;
      parts.push(clip.data);
      timeline.push({ at: Number(elapsed.toFixed(2)), event: `speech: "${phrases[i]}"` });
      elapsed += seconds;

      const gap = i === 0 ? GAP_AFTER_OPENING_S : GAP_AFTER_INTERRUPTION_S;
      parts.push(silence(fmt, gap));
      timeline.push({ at: Number(elapsed.toFixed(2)), event: `silence ${gap}s (closes the utterance)` });
      elapsed += gap;
    });

    parts.push(silence(fmt, TAIL_S));
    timeline.push({ at: Number(elapsed.toFixed(2)), event: `silence ${TAIL_S}s (tail)` });
    elapsed += TAIL_S;

    const wav = writeWav(fmt, Buffer.concat(parts));
    writeFileSync(outPath, wav);
    return {
      path: outPath,
      seconds: Number(elapsed.toFixed(2)),
      sampleRate: fmt.sampleRate,
      channels: fmt.channels,
      utterances: phrases.length,
      timeline,
    };
  } finally {
    rmSync(workDir, { recursive: true, force: true });
  }
}

// Runnable by hand, which is how the track gets listened to when a capture
// looks wrong: `node build-voice-fixture.mjs /tmp/mic.wav`.
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  const target = process.argv[2] ?? join(tmpdir(), "hive-voice-fixture.wav");
  console.log(JSON.stringify(await buildVoiceFixture(target), null, 2));
}
