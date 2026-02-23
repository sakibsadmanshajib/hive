import { randomUUID } from "node:crypto";
import { CreditService } from "./credit-service";
import { ModelService } from "./model-service";
import { UsageService } from "./usage-service";

type ChatMessage = { role: string; content: string };

export class AiService {
  constructor(
    private readonly models: ModelService,
    private readonly credits: CreditService,
    private readonly usage: UsageService,
  ) {}

  chatCompletions(userId: string, modelId: string | undefined, messages: ChatMessage[]) {
    const model = modelId && modelId !== "auto" ? this.models.findById(modelId) : this.models.pickDefault("chat");
    if (!model || model.capability !== "chat") {
      return { error: "unknown model", statusCode: 400 as const };
    }

    const credits = model.creditsPerRequest;
    if (!this.credits.consume(userId, credits)) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }

    const text = messages.map((msg) => msg.content).join(" ").trim();
    this.usage.add({ userId, endpoint: "/v1/chat/completions", model: model.id, credits });

    return {
      statusCode: 200 as const,
      body: {
        id: `chatcmpl_${randomUUID().slice(0, 12)}`,
        object: "chat.completion",
        created: Math.floor(Date.now() / 1000),
        model: model.id,
        choices: [
          {
            index: 0,
            finish_reason: "stop",
            message: {
              role: "assistant",
              content: `MVP response: ${text || "Your request was processed."}`,
            },
          },
        ],
      },
      headers: {
        "x-model-routed": model.id,
        "x-actual-credits": String(credits),
      },
    };
  }

  responses(userId: string, input: string) {
    const model = this.models.pickDefault("chat");
    const credits = Math.max(4, Math.floor(model.creditsPerRequest * 0.75));
    if (!this.credits.consume(userId, credits)) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    this.usage.add({ userId, endpoint: "/v1/responses", model: model.id, credits });

    return {
      statusCode: 200 as const,
      body: {
        id: `resp_${randomUUID().slice(0, 12)}`,
        object: "response",
        model: model.id,
        output: [{ type: "text", text: `MVP output: ${input || "No input provided."}` }],
      },
    };
  }

  imageGeneration(userId: string, prompt: string) {
    const model = this.models.pickDefault("image");
    const credits = model.creditsPerRequest;
    if (!this.credits.consume(userId, credits)) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    this.usage.add({ userId, endpoint: "/v1/images/generations", model: model.id, credits });

    return {
      statusCode: 200 as const,
      headers: { "x-actual-credits": String(credits) },
      body: {
        created: Math.floor(Date.now() / 1000),
        object: "list",
        data: [
          {
            url: `https://example.invalid/generated/${encodeURIComponent(prompt || "image")}.png`,
          },
        ],
      },
    };
  }
}
