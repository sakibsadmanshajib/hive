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

    expect(result.statusCode).toBe(200);
    expect("body" in result && result.body.model).toBe("gpt-4o");
  });

  it("response includes all CHAT-01 required fields", () => {
    const result = aiService.chatCompletions(
      "test-user",
      { model: "gpt-4o", messages: [{ role: "user", content: "hello" }] },
      usageContext,
    );

    expect(result.statusCode).toBe(200);
    if (!("body" in result)) throw new Error("Expected body in result");
    const body = result.body;

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

    expect(result.statusCode).toBe(200);
    if (!("body" in result)) throw new Error("Expected body in result");

    expect(result.body.choices[0].logprobs).toBeNull();
    expect("logprobs" in result.body.choices[0]).toBe(true);
  });

  it("response includes usage object (CHAT-03)", () => {
    const result = aiService.chatCompletions(
      "test-user",
      { model: "gpt-4o", messages: [{ role: "user", content: "test" }] },
      usageContext,
    );

    expect(result.statusCode).toBe(200);
    if (!("body" in result)) throw new Error("Expected body in result");

    expect(result.body.usage).toBeDefined();
    expect(typeof result.body.usage.prompt_tokens).toBe("number");
    expect(typeof result.body.usage.completion_tokens).toBe("number");
    expect(typeof result.body.usage.total_tokens).toBe("number");
  });

  it("returns 400 for unknown model", () => {
    const result = aiService.chatCompletions(
      "test-user",
      { model: "nonexistent-model", messages: [{ role: "user", content: "hi" }] },
      usageContext,
    );

    expect(result.statusCode).toBe(400);
    expect("error" in result && result.error).toBe("unknown model");
  });

  it("returns 402 for insufficient credits", () => {
    // Create a user with no credits
    const result = aiService.chatCompletions(
      "broke-user",
      { model: "gpt-4o", messages: [{ role: "user", content: "hi" }] },
      usageContext,
    );

    expect(result.statusCode).toBe(402);
    expect("error" in result && result.error).toBe("insufficient credits");
  });
});
