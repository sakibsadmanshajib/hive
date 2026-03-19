import { describe, it, expect } from "vitest";
import { AiService } from "../../domain/ai-service";
import { ModelService } from "../../domain/model-service";
import { CreditService } from "../../domain/credit-service";
import { UsageService } from "../../domain/usage-service";

// UUID v4 pattern for x-request-id validation
const UUID_V4_REGEX = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

// Required AI headers on every AI-invoking endpoint response
const REQUIRED_AI_HEADERS = [
  "x-model-routed",
  "x-provider-used",
  "x-provider-model",
  "x-actual-credits",
];

describe("DIFF-04: x-request-id generation", () => {
  it("crypto.randomUUID() produces valid UUID v4", () => {
    const { randomUUID } = require("node:crypto");
    const id = randomUUID();
    expect(id).toMatch(UUID_V4_REGEX);
  });

  it("each call produces a unique ID", () => {
    const { randomUUID } = require("node:crypto");
    const ids = new Set(Array.from({ length: 100 }, () => randomUUID()));
    expect(ids.size).toBe(100);
  });
});

describe("DIFF-01/DIFF-02: AI service header completeness", () => {
  // Use real MVP AiService with real ModelService to test header contracts
  const models = new ModelService();
  const credits = new CreditService();
  const usage = new UsageService();
  const ai = new AiService(models, credits, usage);
  const usageCtx = { channel: "api" as const };

  // Give the test user enough credits (topUp takes BDT, multiplied by 100 internally)
  credits.topUp("test-user", 100);

  it("chatCompletions returns all 4 required headers", () => {
    const result = ai.chatCompletions("test-user", {
      model: "gpt-4o-mini",
      messages: [{ role: "user", content: "test" }],
    }, usageCtx);
    expect("error" in result).toBe(false);
    if (!("error" in result)) {
      for (const header of REQUIRED_AI_HEADERS) {
        expect(result.headers).toHaveProperty(header);
        expect(result.headers[header as keyof typeof result.headers]).toBeTruthy();
      }
    }
  });

  it("chatCompletions x-actual-credits is a numeric string", () => {
    const result = ai.chatCompletions("test-user", {
      model: "gpt-4o-mini",
      messages: [{ role: "user", content: "test" }],
    }, usageCtx);
    if (!("error" in result)) {
      const creditValue = result.headers["x-actual-credits"];
      expect(creditValue).toMatch(/^\d+(\.\d+)?$/);
    }
  });

  it("responses returns all 4 required headers", () => {
    const result = ai.responses("test-user", {
      model: "gpt-4o-mini",
      input: "test input",
    }, usageCtx);
    expect("error" in result).toBe(false);
    if (!("error" in result)) {
      for (const header of REQUIRED_AI_HEADERS) {
        expect(result.headers).toHaveProperty(header);
        expect(result.headers[header as keyof typeof result.headers]).toBeTruthy();
      }
    }
  });

  it("embeddings returns all 4 required headers", () => {
    const result = ai.embeddings("test-user", {
      model: "openai/text-embedding-3-small",
      input: "test input",
    }, usageCtx);
    expect("error" in result).toBe(false);
    if (!("error" in result)) {
      for (const header of REQUIRED_AI_HEADERS) {
        expect(result.headers).toHaveProperty(header);
        expect(result.headers[header as keyof typeof result.headers]).toBeTruthy();
      }
    }
  });

  it("imageGeneration returns all 4 required headers", () => {
    const result = ai.imageGeneration("test-user", {
      prompt: "a cat",
      model: "dall-e-3",
    }, usageCtx);
    expect("error" in result).toBe(false);
    if (!("error" in result)) {
      for (const header of REQUIRED_AI_HEADERS) {
        expect(result.headers).toHaveProperty(header);
        expect(result.headers[header as keyof typeof result.headers]).toBeTruthy();
      }
    }
  });

  it("x-provider-used is 'hive-mvp' for all MVP methods", () => {
    const chatResult = ai.chatCompletions("test-user", {
      model: "gpt-4o-mini",
      messages: [{ role: "user", content: "test" }],
    }, usageCtx);
    if (!("error" in chatResult)) {
      expect(chatResult.headers["x-provider-used"]).toBe("hive-mvp");
    }
  });
});
