import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
// chat-completions.test.ts already exercises tool_choice: {type:"function"}.
// This file covers the remaining tool_choice enum values, parallel tool
// calls, and the multi-turn tool-result round trip. Same pinned alias, same
// reason (see that file's header comment).
const TOOL_CAPABLE_MODEL =
  process.env.HIVE_TOOLS_MODEL ?? "hive-small";

const WEATHER_TOOL: OpenAI.Chat.Completions.ChatCompletionTool = {
  type: "function",
  function: {
    name: "get_weather",
    description: "Get the current weather for a location",
    parameters: {
      type: "object",
      properties: {
        location: { type: "string", description: "The city to get weather for" },
      },
      required: ["location"],
    },
  },
};

describe("Chat Completions tool_choice variants", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it('tool_choice: "none" never invokes a tool even when tools are offered', async () => {
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [{ role: "user", content: "What is 2+2? Answer in words." }],
      max_tokens: 64,
      tools: [WEATHER_TOOL],
      tool_choice: "none",
    });

    expect(response.choices[0].message.tool_calls).toBeUndefined();
    expect(response.choices[0].finish_reason).not.toBe("tool_calls");
  });

  it('tool_choice: "auto" lets the model decide', async () => {
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [{ role: "user", content: "What is the weather in Dhaka?" }],
      max_tokens: 256,
      tools: [WEATHER_TOOL],
      tool_choice: "auto",
    });

    expect(response.object).toBe("chat.completion");
    // "auto" only asserts the request round-trips cleanly; the model's own
    // routing decision (tool call vs text) is not asserted either way.
  });

  it('tool_choice: "required" forces a tool call', async () => {
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [{ role: "user", content: "Hello there" }],
      max_tokens: 256,
      tools: [WEATHER_TOOL],
      tool_choice: "required",
    });

    expect(response.choices[0].finish_reason).toBe("tool_calls");
    expect(response.choices[0].message.tool_calls?.length).toBeGreaterThan(0);
  });

  it("parallel_tool_calls: true can return more than one tool call", async () => {
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [
        {
          role: "user",
          content: "What is the weather in Dhaka and in London? Call the tool once per city.",
        },
      ],
      max_tokens: 512,
      tools: [WEATHER_TOOL],
      tool_choice: "required",
      parallel_tool_calls: true,
    });

    const toolCalls = response.choices[0].message.tool_calls ?? [];
    expect(toolCalls.length).toBeGreaterThanOrEqual(1);
    // Not asserting toolCalls.length === 2: whether a given routed model
    // actually emits parallel calls for this prompt is a model behavior, not
    // a gateway contract. The gateway contract under test is that the
    // parameter round-trips without being silently dropped or 500ing, and
    // that every returned call has a well-formed id/function shape.
    for (const call of toolCalls) {
      expect(call.id).toBeTruthy();
      // openai@7's ChatCompletionMessageToolCall is a union of function and
      // custom tool calls; WEATHER_TOOL is function-typed so every call this
      // model returns for it must narrow to the function variant.
      expect(call.type).toBe("function");
      if (call.type === "function") {
        expect(call.function.name).toBe("get_weather");
        expect(() => JSON.parse(call.function.arguments)).not.toThrow();
      }
    }
  });

  it("completes a full multi-turn tool call and tool result round trip", async () => {
    const messages: OpenAI.Chat.Completions.ChatCompletionMessageParam[] = [
      { role: "user", content: "What is the weather in Dhaka?" },
    ];

    const first = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages,
      max_tokens: 256,
      tools: [WEATHER_TOOL],
      tool_choice: "required",
    });

    const toolCall = first.choices[0].message.tool_calls?.[0];
    expect(toolCall).toBeDefined();

    // Append the assistant's tool-call turn, then the tool's result, exactly
    // as an agent framework would before asking for the final answer.
    messages.push(first.choices[0].message);
    messages.push({
      role: "tool",
      tool_call_id: toolCall!.id,
      content: JSON.stringify({ location: "Dhaka", temperature_c: 31, condition: "humid" }),
    });

    const second = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages,
      // 512, not 256. The second turn is where this alias does its thinking:
      // measured live on 2026-08-29 over 30 attempts at 256, reasoning tokens
      // ran 22 to 182 out of 49 to 231 total, so the visible answer survives
      // on a margin that occasionally is not there. That tail is what made
      // this test fail intermittently in CI. The gateway now retries an empty
      // length completion whatever route produced it, and this ceiling stops
      // the suite from provoking the case on nearly every run.
      max_tokens: 512,
    });

    expect(second.object).toBe("chat.completion");
    expect(typeof second.choices[0].message.content).toBe("string");
    // Unchanged and deliberately strict. The final turn of a tool round trip
    // is what an agent framework reads back to the user, and an empty string
    // there is a failure however it was produced.
    expect(second.choices[0].message.content).toBeTruthy();
  });
});
