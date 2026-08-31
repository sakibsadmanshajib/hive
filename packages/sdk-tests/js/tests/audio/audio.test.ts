import { describe, it, expect } from "vitest";
import OpenAI, { toFile } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
// Real Groq-backed routes, seeded in
// supabase/migrations/20260717_02_voice_groq_stt_tts.sql: hive-tts ->
// canopylabs/orpheus-v1-english, hive-stt -> whisper-large-v3.
const TTS_MODEL = process.env.HIVE_TTS_MODEL ?? "hive-tts";
const STT_MODEL = process.env.HIVE_STT_MODEL ?? "hive-stt";


// A hand-built one second, 16 kHz, mono, silent WAV. It exists so the
// transcription endpoint can be exercised WITHOUT first calling speech
// synthesis. That coupling is not hypothetical harm: through #1318 and #1381
// the round trip below threw on the speech call and never reached
// transcription at all, so for months any defect in the transcription route
// would have sat behind an expected-failure marker unnoticed. Both are fixed
// and the markers are gone, and this stays split for the next time.
function silentWav(seconds: number): Buffer {
  const sampleRate = 16000;
  const dataBytes = sampleRate * seconds * 2;
  const buf = Buffer.alloc(44 + dataBytes);
  buf.write("RIFF", 0, "ascii");
  buf.writeUInt32LE(36 + dataBytes, 4);
  buf.write("WAVE", 8, "ascii");
  buf.write("fmt ", 12, "ascii");
  buf.writeUInt32LE(16, 16);
  buf.writeUInt16LE(1, 20);
  buf.writeUInt16LE(1, 22);
  buf.writeUInt32LE(sampleRate, 24);
  buf.writeUInt32LE(sampleRate * 2, 28);
  buf.writeUInt16LE(2, 32);
  buf.writeUInt16LE(16, 34);
  buf.write("data", 36, "ascii");
  buf.writeUInt32LE(dataBytes, 40);
  return buf;
}

describe("Audio", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  // Issue #1381, which was what was underneath #1318, is fixed: the edge now
  // resolves response_format against what the route can actually produce, so
  // the SDK's omission no longer lets the upstream fall back to its own mp3
  // default and refuse the request. Both halves of the compatibility promise
  // are live, so both of the expected-failure markers this file used to carry
  // are gone rather than repointed at a third issue.
  //
  // The voice below stays the OpenAI default rather than one the current
  // upstream happens to accept: the point of this suite is what an unmodified
  // OpenAI SDK can do, and swapping in a provider-specific voice would stop
  // measuring that. Same for the absent response_format, which is the exact
  // parameter #1381 was about: adding one here would stop measuring it.
  it("audio.speech.create returns non-empty binary audio", async () => {
    const response = await client.audio.speech.create({
      model: TTS_MODEL,
      voice: "alloy",
      input: "This is a Hive gateway conformance check.",
    });

    const buffer = Buffer.from(await response.arrayBuffer());
    expect(buffer.length).toBeGreaterThan(0);
  });

  it("audio.transcriptions.create accepts audio and answers in the OpenAI shape", async () => {
    // Silence transcribes to an empty string on some models and to a filler
    // token on others, so the assertion is the SHAPE, not the words: this
    // test is here to prove the endpoint accepts a real multipart upload and
    // answers the documented envelope, independent of the speech route.
    const transcription = await client.audio.transcriptions.create({
      model: STT_MODEL,
      file: await toFile(silentWav(1), "silence.wav", { type: "audio/wav" }),
    });

    expect(typeof transcription.text).toBe("string");
  });

  // Unblocked by the same fix: this round trip needs the speech call above to
  // produce something before it has anything to transcribe. The transcription
  // half is still exercised independently by the test above it, so a defect
  // there cannot hide behind this one.
  it("audio.transcriptions.create round-trips speech back to text", async () => {
    const speech = await client.audio.speech.create({
      model: TTS_MODEL,
      voice: "alloy",
      input: "The quick brown fox jumps over the lazy dog.",
    });
    const audioBuffer = Buffer.from(await speech.arrayBuffer());

    // Named and typed for what the bytes are. The gateway asks the TTS route
    // for the one container it can produce (wav, #1381), so calling this
    // speech.mp3 would hand the transcription provider a filename and a MIME
    // type that disagree with its own content, which is a needless way to make
    // a real round trip fail on something other than what it is measuring.
    const transcription = await client.audio.transcriptions.create({
      model: STT_MODEL,
      file: await toFile(audioBuffer, "speech.wav", { type: "audio/wav" }),
    });

    expect(typeof transcription.text).toBe("string");
    expect(transcription.text.trim().length).toBeGreaterThan(0);
  });
});
