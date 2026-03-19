import { describe, it, expect, beforeEach } from "vitest";
import { AiService } from "../ai-service";
import { ModelService } from "../model-service";
import { CreditService } from "../credit-service";
import { UsageService } from "../usage-service";

describe("AiService.chatCompletions", () => {
  let aiService: AiService;
  let modelService: ModelService;
  let creditService: CreditService;
  let usageService: UsageService;

  const usageContext = { channel: "api" as const, apiKeyId: "key-1" };

  beforeEach(() => {
    modelService = new ModelService();
    creditService = new CreditService();
    usageService = new UsageService();
    aiService = new AiService(modelService, creditService, usageService);
    // Ensure user has credits
    creditService.topUp("test-user", 100);
  });

  it("accepts full body object with model and messages", () => {
    const result = aiService.chatCompletions(
      "test-user",
      { model: "gpt-4o", messages: [{ role: "user", content: "hi" }] },
      usageContext,
    );

    if (result.statusCode !== 200) throw new Error(`Expected 200, got ${result.statusCode}`);
    expect(result.body.model).toBe("gpt-4o");
  });

  it("response includes all CHAT-01 required fields", () => {
    const result = aiService.chatCompletions(
      "test-user",
      { model: "gpt-4o", messages: [{ role: "user", content: "hello" }] },
      usageContext,
    );

    if (result.statusCode !== 200) throw new Error(`Expected 200, got ${result.statusCode}`);
    const { body } = result;

    expect(body.id).toMatch(/^chatcmpl_/);
    expect(body.object).toBe("chat.completion");
    expect(typeof body.created).toBe("number");
    expect(body.model).toBe("gpt-4o");
    expect(body.choices.length).toBeGreaterThanOrEqual(1);
    expect(body.choices[0].index).toBe(0);
    expect(body.choices[0].finish_reason).toBe("stop");
    expect(body.choices[0].message.role).toBe("assistant");
    expect(typeof body.choices[0].message.content).toBe("string");
  });

  it("choices include logprobs: null (CHAT-01)", () => {
    const result = aiService.chatCompletions(
      "test-user",
      { model: "gpt-4o", messages: [{ role: "user", content: "test" }] },
      usageContext,
    );

    if (result.statusCode !== 200) throw new Error(`Expected 200, got ${result.statusCode}`);
    const { body } = result;

    expect(body.choices[0].logprobs).toBeNull();
    expect("logprobs" in body.choices[0]).toBe(true);
  });

  it("response includes usage object (CHAT-03)", () => {
    const result = aiService.chatCompletions(
      "test-user",
      { model: "gpt-4o", messages: [{ role: "user", content: "test" }] },
      usageContext,
    );

    if (result.statusCode !== 200) throw new Error(`Expected 200, got ${result.statusCode}`);
    const { body } = result;

    expect(body.usage).toBeDefined();
    expect(typeof body.usage.prompt_tokens).toBe("number");
    expect(typeof body.usage.completion_tokens).toBe("number");
    expect(typeof body.usage.total_tokens).toBe("number");
  });

  it("returns 400 for unknown model", () => {
    const result = aiService.chatCompletions(
      "test-user",
      { model: "nonexistent-model", messages: [{ role: "user", content: "hi" }] },
      usageContext,
    );

    if (result.statusCode !== 400) throw new Error(`Expected 400, got ${result.statusCode}`);
    expect(result.error).toBe("unknown model");
  });

  it("returns 402 for insufficient credits", () => {
    // Create a user with no credits
    const result = aiService.chatCompletions(
      "broke-user",
      { model: "gpt-4o", messages: [{ role: "user", content: "hi" }] },
      usageContext,
    );

    if (result.statusCode !== 402) throw new Error(`Expected 402, got ${result.statusCode}`);
    expect(result.error).toBe("insufficient credits");
  });
});
