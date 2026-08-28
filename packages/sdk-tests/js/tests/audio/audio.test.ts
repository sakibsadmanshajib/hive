import { describe, it, expect } from "vitest";
import OpenAI, { toFile } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
// Real Groq-backed routes, seeded in
// supabase/migrations/20260717_02_voice_groq_stt_tts.sql: hive-tts ->
// canopylabs/orpheus-v1-english, hive-stt -> whisper-large-v3.
const TTS_MODEL = process.env.HIVE_TTS_MODEL ?? "hive-tts";
const STT_MODEL = process.env.HIVE_STT_MODEL ?? "hive-stt";

describe("Audio", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("audio.speech.create returns non-empty binary audio", async () => {
    const response = await client.audio.speech.create({
      model: TTS_MODEL,
      voice: "alloy",
      input: "This is a Hive gateway conformance check.",
    });

    const buffer = Buffer.from(await response.arrayBuffer());
    expect(buffer.length).toBeGreaterThan(0);
  });

  it("audio.transcriptions.create round-trips speech back to text", async () => {
    const speech = await client.audio.speech.create({
      model: TTS_MODEL,
      voice: "alloy",
      input: "The quick brown fox jumps over the lazy dog.",
    });
    const audioBuffer = Buffer.from(await speech.arrayBuffer());

    const transcription = await client.audio.transcriptions.create({
      model: STT_MODEL,
      file: await toFile(audioBuffer, "speech.mp3", { type: "audio/mpeg" }),
    });

    expect(typeof transcription.text).toBe("string");
    expect(transcription.text.trim().length).toBeGreaterThan(0);
  });
});
