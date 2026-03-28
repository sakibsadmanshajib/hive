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
    body: { model?: string; messages?: Array<{ role: string; content: string }>; [key: string]: unknown },
    usageContext: UsageContext,
  ) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const modelId = body.model;
    const messages = (body.messages ?? []) as ChatMessage[];
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
              refusal: null,
            },
            logprobs: null,
          },
        ],
        usage: {
          prompt_tokens: 0,
          completion_tokens: 0,
          total_tokens: 0,
        },
      },
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": "hive-mvp",
        "x-provider-model": model.id,
        "x-actual-credits": String(credits),
      },
    };
  }

  responses(userId: string, body: { model?: string; input: string | unknown[]; instructions?: string; [key: string]: unknown }, usageContext: UsageContext) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const model = body.model
      ? this.models.findById(body.model)
      : this.models.pickDefault("chat");
    if (!model || model.capability !== "chat") {
      return { error: `Unknown model: ${body.model}`, statusCode: 400 as const };
    }
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

    const inputText = typeof body.input === "string" ? body.input : JSON.stringify(body.input);
    return {
      statusCode: 200 as const,
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": "hive-mvp",
        "x-provider-model": model.id,
        "x-actual-credits": String(credits),
      },
      body: {
        id: `resp_${randomUUID().replace(/-/g, "").slice(0, 24)}`,
        object: "response" as const,
        created_at: Math.floor(Date.now() / 1000),
        status: "completed" as const,
        model: model.id,
        output: [{
          type: "message" as const,
          id: `msg_${randomUUID().replace(/-/g, "").slice(0, 24)}`,
          role: "assistant" as const,
          status: "completed" as const,
          content: [{
            type: "output_text" as const,
            text: `MVP output: ${inputText || "No input provided."}`,
          }],
        }],
        usage: {
          input_tokens: 0,
          output_tokens: 0,
          total_tokens: 0,
        },
      },
    };
  }

  embeddings(
    userId: string,
    body: { model: string; input: string | string[]; encoding_format?: "float" | "base64"; dimensions?: number; user?: string },
    usageContext: UsageContext,
  ) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const model = this.models.findById(body.model);
    if (!model || model.capability !== "embedding") {
      return { error: `Unknown embedding model: ${body.model}`, statusCode: 400 as const };
    }
    const credits = this.models.creditsForRequest(model);
    if (!this.credits.consume(userId, credits)) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    this.usage.add({
      userId,
      endpoint: "/v1/embeddings",
      model: model.id,
      credits,
      ...resolvedUsageContext,
    });
    const inputArray = Array.isArray(body.input) ? body.input : [body.input];
    return {
      statusCode: 200 as const,
      body: {
        object: "list" as const,
        data: inputArray.map((_, index) => ({
          object: "embedding" as const,
          embedding: [0.0, 0.0, 0.0],
          index,
        })),
        model: model.id,
        usage: { prompt_tokens: 0, total_tokens: 0 },
      },
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": "hive-mvp",
        "x-provider-model": model.id,
        "x-actual-credits": String(credits),
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
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": "hive-mvp",
        "x-provider-model": model.id,
        "x-actual-credits": String(credits),
      },
      body: {
        created: Math.floor(Date.now() / 1000),
        data: [
          {
            url: `https://placeholder.test/generated/${encodeURIComponent(prompt || "image")}.png`,
          },
        ],
      },
    };
  }
}
