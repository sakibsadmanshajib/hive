import { describe, it, expect } from "vitest";
import { ProviderCostCalculator } from "../../src/providers/cost-calculator";

describe("ProviderCostCalculator", () => {
  const calculator = new ProviderCostCalculator();

  it("should calculate OpenRouter credits correctly", () => {
    const tokens = { input: 1000, output: 500 };
    const model = "openai/gpt-3.5-turbo";
    
    const credits = calculator.calculateCredits("openrouter", model, tokens);
    expect(credits).toBeGreaterThan(0);
    expect(credits).toBeLessThanOrEqual(16); // Max credits for standard models
  });

  it("should handle unknown providers with fallback", () => {
    const tokens = { input: 1000, output: 500 };
    const credits = calculator.calculateCredits("unknown", "model", tokens);
    expect(credits).toBe(8); // Default fallback
  });

  it("should calculate credits for providers without token costs", () => {
    const tokens = { input: 1000, output: 500 };
    const credits = calculator.calculateCredits("ollama", "llama3.1:8b", tokens);
    expect(credits).toBe(8); // Fixed cost for Ollama
  });

  it("should estimate tokens for messages correctly", () => {
    const messages = [
      { role: "user", content: "Hello, how are you today?" },
      { role: "assistant", content: "I'm doing well, thank you for asking." },
    ];
    
    const estimatedTokens = calculator.estimateTokens(messages);
    expect(estimatedTokens).toBeGreaterThan(0);
    expect(estimatedTokens).toBeLessThan(100); // Reasonable estimate
  });

  it("should handle empty messages in token estimation", () => {
    const messages: any[] = [];
    const estimatedTokens = calculator.estimateTokens(messages);
    expect(estimatedTokens).toBe(0);
  });

  it("should round up fractional credits", () => {
    const tokens = { input: 100, output: 50 }; // Very small token count
    const credits = calculator.calculateCredits("openrouter", "model", tokens);
    expect(Number.isInteger(credits)).toBe(true);
    expect(credits).toBeGreaterThanOrEqual(1);
  });
});