import { randomUUID } from "node:crypto";
import { CreditService } from "./credit-service";
import { ModelService } from "./model-service";
import type { UsageChannel } from "./types";
import { UsageService } from "./usage-service";

type ChatMessage = { role: string; content: string };
type UsageContext = { channel: UsageChannel; apiKeyId?: string };
type ImageRequest = {
  prompt: string;
  model?: string;
  n?: number;
  size?: string;
  responseFormat?: "url" | "b64_json";
  user?: string;
};

export class AiService {
  constructor(
    private readonly models: ModelService,
    private readonly credits: CreditService,
    private readonly usage: UsageService,
  ) {}

  private buildUsageContext(context: UsageContext) {
    return {
      channel: context.channel,
      apiKeyId: context.apiKeyId,
    };
  }

  chatCompletions(
    userId: string,
    modelId: string | undefined,
    messages: ChatMessage[],
    usageContext: UsageContext,
  ) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const model = modelId && modelId !== "auto" ? this.models.findById(modelId) : this.models.pickDefault("chat");
    if (!model || model.capability !== "chat") {
      return { error: "unknown model", statusCode: 400 as const };
    }

    const credits = this.models.creditsForRequest(model);
    if (!this.credits.consume(userId, credits)) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }

    const text = messages.map((msg) => msg.content).join(" ").trim();
    this.usage.add({
      userId,
      endpoint: "/v1/chat/completions",
      model: model.id,
      credits,
      ...resolvedUsageContext,
    });

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

  responses(userId: string, input: string, usageContext: UsageContext) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const model = this.models.pickDefault("chat");
    const credits = Math.max(4, Math.floor(this.models.creditsForRequest(model) * 0.75));
    if (!this.credits.consume(userId, credits)) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    this.usage.add({
      userId,
      endpoint: "/v1/responses",
      model: model.id,
      credits,
      ...resolvedUsageContext,
    });

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

  imageGeneration(userId: string, input: string | ImageRequest, usageContext: UsageContext) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const requestedModel = typeof input === "string" ? undefined : input.model;
    const model = requestedModel && requestedModel !== "auto"
      ? this.models.findById(requestedModel)
      : this.models.pickDefault("image");
    if (!model || model.capability !== "image") {
      return { error: "unknown model", statusCode: 400 as const };
    }
    const credits = this.models.creditsForRequest(model);
    if (!this.credits.consume(userId, credits)) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    this.usage.add({
      userId,
      endpoint: "/v1/images/generations",
      model: model.id,
      credits,
      ...resolvedUsageContext,
    });
    const prompt = typeof input === "string" ? input : input.prompt;

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
