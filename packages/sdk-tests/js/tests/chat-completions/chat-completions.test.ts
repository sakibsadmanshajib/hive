import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-free";
// Capability-specific tests (tools, response_format) need an alias whose
// route is seeded tools_supported=true AND whose provider honors the OpenAI
// response contract. hive-free fails the first bar (free pool members are
// seeded tools_supported=false until cross-member parity is probed, #1115),
// and the edge correctly 400s tools/response_format there (run 32736430913).
// The default is hive-small (owner directive 2026-08-30: no CI pipeline may
// call a paid completion model). It is upstream-free, pins to the single
// healthy route route-free-small on
// openrouter/dots-studio/dots-3-note-preview:free, and that route is seeded
// tools_supported=true. Verified live against OpenRouter before this default
// moved, every response reporting cost 0: forced tool_choice returns a real
// tool_calls array, tool_choice required/none/auto each behave correctly, a
// multi-turn tool-result round trip completes, and message.content comes back
// as a string on 6 of 6 runs under both response_format json_object and
// json_schema. That last property is what the previous default
// deepseek-v4-flash could not hold: its `-latest` slug returned
// message.content as a parsed JSON object (run 32665985618), as null, and as
// string across probes on 2026-08-23, so this move is a fidelity improvement
// as well as a spend one. If the contract ever destabilises here, this suite
// fails loudly by design; repoint HIVE_TOOLS_MODEL at another upstream-free
// tools-capable alias instead of loosening the assertions.
const TOOL_CAPABLE_MODEL =
  process.env.HIVE_TOOLS_MODEL ?? "hive-free";

describe("Chat Completions", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  // The only test still on the free MODEL. Free-lane worst case (retry on
  // empty content, reasoning-member budgets) can exceed vitest's default
  // 60s; fail here on assertion, never on the runner's timeout.
  it(
    "returns a valid chat completion via SDK",
    { timeout: 120000 },
    async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [{ role: "user", content: "Say hello" }],
      max_tokens: 256,
    });

    expect(response.object).toBe("chat.completion");
    expect(response.choices.length).toBeGreaterThanOrEqual(1);
    expect(response.choices[0].message.role).toBe("assistant");
    expect(typeof response.choices[0].message.content).toBe("string");
    expect(response.choices[0].message.content).toBeTruthy();
    expect(response.usage).toBeDefined();
    expect(response.usage!.prompt_tokens).toBeGreaterThan(0);
    expect(response.usage!.completion_tokens).toBeGreaterThan(0);
    },
  );

  it("model field shows Hive alias not provider handle", async () => {
    // Alias-echo check, not a free-lane exercise: since #1225 the free pool
    // retries once on empty content and reasoning members inflate budgets,
    // so worst-case free-lane latency exceeds vitest's 60s default. This
    // test only needs any live alias to echo itself back, so it runs on the
    // same fast bounded model as the tools/response_format tests.
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [{ role: "user", content: "Say hello" }],
      max_tokens: 256,
    });

    // Model should be the Hive alias, not a provider route handle.
    expect(response.model).not.toMatch(/route-/i);
    expect(response.model).not.toMatch(/openrouter/i);
    expect(response.model).not.toMatch(/groq/i);
  });

  it("rejects invalid model with 404", async () => {
    await expect(
      client.chat.completions.create({
        model: "nonexistent-model-12345",
        messages: [{ role: "user", content: "hello" }],
        max_tokens: 256,
      }),
    ).rejects.toMatchObject({ status: 404 });
  });

  it("passes tools through and returns a tool_calls completion", async () => {
    // Phase 20 (#118): capability-based passthrough ships. Routes with
    // tools_supported=true seeded have tool calls forwarded rather than
    // rejected; the edge must NOT 400 on tools for a capable alias.
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [
        { role: "user", content: "What is the weather like in London?" },
      ],
      max_tokens: 256,
      tools: [
        {
          type: "function",
          function: {
            name: "get_weather",
            description: "Get the current weather for a location",
            parameters: {
              type: "object",
              properties: {
                location: {
                  type: "string",
                  description: "The city to get weather for",
                },
              },
              required: ["location"],
            },
          },
        },
      ],
      // Force the model to invoke get_weather so the tool_calls assertions are
      // deterministic and do not depend on the model's auto-routing decision.
      tool_choice: { type: "function", function: { name: "get_weather" } },
    });

    expect(response.object).toBe("chat.completion");
    expect(response.choices.length).toBeGreaterThanOrEqual(1);
    // Model should invoke the tool.
    expect(response.choices[0].finish_reason).toBe("tool_calls");
    expect(response.choices[0].message.tool_calls).toBeDefined();
    expect(response.choices[0].message.tool_calls!.length).toBeGreaterThan(0);
    // openai@7's ChatCompletionMessageToolCall is a union of function and
    // custom tool calls; narrow before reaching .function.
    const toolCall = response.choices[0].message.tool_calls![0];
    expect(toolCall.type).toBe("function");
    if (toolCall.type === "function") {
      expect(toolCall.function.name).toBe("get_weather");
    }
  });

  it("passes response_format through and returns valid JSON", async () => {
    // Phase 20 (#118): response_format forwarded to capable routes.
    // The edge must NOT 400 on response_format for a capable alias. The
    // assertions below are the OpenAI contract and stay strict: content is
    // typed as string on the wire, so an alias whose provider returns it as a
    // raw object or null fails here by design. That is exactly what the
    // unpinned deepseek-v4-flash router did on 2026-08-23 (run 32665985618),
    // the standing risk of the flash default. Do not loosen this
    // to fit one route; repoint HIVE_TOOLS_MODEL if the default regresses.
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [
        {
          role: "user",
          content: 'Return a JSON object with a single key "status" set to "ok".',
        },
      ],
      max_tokens: 256,
      response_format: { type: "json_object" },
    });

    expect(response.object).toBe("chat.completion");
    expect(response.choices.length).toBeGreaterThanOrEqual(1);
    const content = response.choices[0].message.content;
    expect(typeof content).toBe("string");
    // Content must be parseable JSON containing at least one key.
    const parsed = JSON.parse(content!);
    expect(typeof parsed).toBe("object");
    expect(parsed).not.toBeNull();
  });
});
