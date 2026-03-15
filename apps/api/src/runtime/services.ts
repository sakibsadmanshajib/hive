import { randomUUID } from "node:crypto";
import type {
  CreditBalance,
  SessionUserIdentity,
  TrafficAnalyticsSnapshot,
  UsageChannel,
  UsageDailyTrendPoint,
  UsageEvent,
  UsageSummary,
  UsageSummaryBucket,
} from "../domain/types";
import { ModelService } from "../domain/model-service";
import { getEnv, type AppEnv } from "../config/env";
import { BkashAdapter, SslcommerzAdapter } from "./provider-adapters";
import { AnthropicProviderClient } from "../providers/anthropic-client";
import { GeminiProviderClient } from "../providers/gemini-client";
import { GroqProviderClient } from "../providers/groq-client";
import { MockProviderClient } from "../providers/mock-client";
import { OpenAIProviderClient } from "../providers/openai-client";
import { OpenRouterProviderClient } from "../providers/openrouter-client";
import { buildProviderOfferCatalog } from "../providers/provider-offers";
import { OllamaProviderClient } from "../providers/ollama-client";
import { ProviderRegistry } from "../providers/registry";
import type { ProviderName, ProviderReadinessStatus } from "../providers/types";
import type { PersistentApiKey, PersistentApiKeyEvent, PersistentPaymentIntent } from "../domain/types";
import { bdtToCredits } from "../domain/credits-conversion";
import { RedisRateLimiter } from "./redis-rate-limiter";
import { createApiKey } from "./security";
import { LangfuseClient } from "./langfuse";
import { AuthorizationService } from "./authorization";
import { UserSettingsService } from "./user-settings";
import { createSupabaseAdminClient } from "./supabase-client";
import { SupabaseAuthService } from "./supabase-auth-service";
import { SupabaseApiKeyStore } from "./supabase-api-key-store";
import { SupabaseUserStore } from "./supabase-user-store";
import { SupabaseBillingStore } from "./supabase-billing-store";
import { PaymentReconciliationService } from "./payment-reconciliation";
import { PaymentReconciliationScheduler } from "./payment-reconciliation-scheduler";
import { SupabaseGuestAttributionStore } from "./supabase-guest-attribution-store";

type ChatMessage = { role: string; content: string };
type ImageGenerationRequest = {
  model?: string;
  prompt: string;
  n?: number;
  size?: string;
  responseFormat?: "url" | "b64_json";
  user?: string;
};

type ApiKeyStore = {
  create(input: {
    key: string;
    userId: string;
    scopes: string[];
    nickname: string;
    expiresAt?: string;
  }): Promise<void>;
  resolve(key: string): Promise<{ apiKeyId: string; userId: string; scopes: string[] } | null>;
  list(userId: string): Promise<PersistentApiKey[]>;
  revoke(key: string, userId: string): Promise<boolean>;
  revokeById(id: string, userId: string): Promise<boolean>;
  get(key: string): Promise<PersistentApiKey | undefined>;
  listEvents(userId: string): Promise<PersistentApiKeyEvent[]>;
};

type BillingStore = {
  getBalance(userId: string): Promise<CreditBalance>;
  consumeCredits(userId: string, credits: number, referenceId: string): Promise<boolean>;
  refundCredits(userId: string, credits: number, referenceId: string): Promise<CreditBalance>;
  topUpCredits(userId: string, credits: number, referenceId: string): Promise<CreditBalance>;
  createPaymentIntent(intent: PersistentPaymentIntent): Promise<void>;
  getPaymentIntent(intentId: string): Promise<PersistentPaymentIntent | undefined>;
  recordPaymentEvent(
    eventKey: string,
    intentId: string,
    provider: string,
    providerTxnId: string,
    verified: boolean,
  ): Promise<boolean>;
  markPaymentCredited(intentId: string, mintedCredits: number, status: "credited" | "failed"): Promise<void>;
  claimPaymentIntent(
    intentId: string,
    provider: string,
    providerTxnId: string,
  ): Promise<{ success: boolean; intent?: PersistentPaymentIntent; error?: string }>;
  listRecentSnapshot(since: Date): Promise<import("../domain/types").PaymentReconciliationSnapshot>;
};

type GuestAttributionStore = {
  upsertSession(input: {
    guestId: string;
    expiresAt: string;
    lastSeenIp?: string;
  }): Promise<void>;
  addUsage(input: {
    guestId: string;
    endpoint: string;
    model: string;
    credits: number;
    ipAddress?: string;
  }): Promise<unknown>;
  linkGuestToUser(input: {
    guestId: string;
    userId: string;
    linkSource: string;
  }): Promise<void>;
};

class PersistentCreditService {
  constructor(private readonly store: BillingStore) { }

  getBalance(userId: string): Promise<CreditBalance> {
    return this.store.getBalance(userId);
  }

  consume(userId: string, credits: number, referenceId = `req_${randomUUID()}`): Promise<boolean> {
    return this.store.consumeCredits(userId, credits, referenceId);
  }

  refund(userId: string, credits: number, referenceId: string): Promise<CreditBalance> {
    return this.store.refundCredits(userId, credits, referenceId);
  }

  topUp(userId: string, bdtAmount: number, referenceId: string): Promise<CreditBalance> {
    const mintedCredits = bdtToCredits(bdtAmount);
    return this.store.topUpCredits(userId, mintedCredits, referenceId);
  }
}

export class PersistentUsageService {
  constructor(private readonly supabase: ReturnType<typeof createSupabaseAdminClient>) { }

  private async listLinkedGuestIds(guestIds: string[]): Promise<Set<string>> {
    const linkedGuestIds = new Set<string>();
    const chunkSize = 200;

    for (let index = 0; index < guestIds.length; index += chunkSize) {
      const chunk = guestIds.slice(index, index + chunkSize);
      const { data, error } = await this.supabase
        .from("guest_user_links")
        .select("guest_id")
        .in("guest_id", chunk);

      if (error) {
        throw new Error(`failed to read guest link analytics: ${error.message}`);
      }

      for (const row of data ?? []) {
        linkedGuestIds.add(String(row.guest_id));
      }
    }

    return linkedGuestIds;
  }

  private buildWindowStart(windowDays: number): string {
    const start = new Date();
    start.setUTCHours(0, 0, 0, 0);
    start.setUTCDate(start.getUTCDate() - (windowDays - 1));
    return start.toISOString();
  }

  private buildSummary(events: UsageEvent[], windowDays: number): UsageSummary {
    const daily = new Map<string, UsageDailyTrendPoint>();
    const byModel = new Map<string, UsageSummaryBucket>();
    const byEndpoint = new Map<string, UsageSummaryBucket>();
    const byChannel = new Map<string, UsageSummaryBucket>();
    const byApiKey = new Map<string, UsageSummaryBucket>();
    const now = new Date();
    now.setUTCHours(0, 0, 0, 0);

    for (let index = windowDays - 1; index >= 0; index -= 1) {
      const date = new Date(now);
      date.setUTCDate(date.getUTCDate() - index);
      const key = date.toISOString().slice(0, 10);
      daily.set(key, { date: key, requests: 0, credits: 0 });
    }

    const filteredEvents = events.filter((event) => daily.has(event.createdAt.slice(0, 10)));
    const totalRequests = filteredEvents.length;
    const totalCredits = filteredEvents.reduce((sum, event) => sum + event.credits, 0);

    for (const event of filteredEvents) {
      const dateKey = event.createdAt.slice(0, 10);
      const day = daily.get(dateKey);
      if (day) {
        day.requests += 1;
        day.credits += event.credits;
      }

      const modelBucket = byModel.get(event.model) ?? { key: event.model, requests: 0, credits: 0 };
      modelBucket.requests += 1;
      modelBucket.credits += event.credits;
      byModel.set(event.model, modelBucket);

      const endpointBucket = byEndpoint.get(event.endpoint) ?? { key: event.endpoint, requests: 0, credits: 0 };
      endpointBucket.requests += 1;
      endpointBucket.credits += event.credits;
      byEndpoint.set(event.endpoint, endpointBucket);

      const channelBucket = byChannel.get(event.channel) ?? { key: event.channel, requests: 0, credits: 0 };
      channelBucket.requests += 1;
      channelBucket.credits += event.credits;
      byChannel.set(event.channel, channelBucket);

      if (event.apiKeyId) {
        const apiKeyBucket = byApiKey.get(event.apiKeyId) ?? { key: event.apiKeyId, requests: 0, credits: 0 };
        apiKeyBucket.requests += 1;
        apiKeyBucket.credits += event.credits;
        byApiKey.set(event.apiKeyId, apiKeyBucket);
      }
    }

    const sortBuckets = (entries: UsageSummaryBucket[]) => entries.sort((left, right) => {
      if (right.credits !== left.credits) {
        return right.credits - left.credits;
      }
      return left.key.localeCompare(right.key);
    });

    return {
      windowDays,
      totalRequests,
      totalCredits,
      daily: [...daily.values()],
      byModel: sortBuckets([...byModel.values()]),
      byEndpoint: sortBuckets([...byEndpoint.values()]),
      byChannel: sortBuckets([...byChannel.values()]),
      byApiKey: sortBuckets([...byApiKey.values()]),
    };
  }

  async add(entry: Omit<UsageEvent, "id" | "createdAt" | "channel"> & { channel?: UsageChannel }): Promise<UsageEvent> {
    const channel = entry.channel ?? (entry.userId === "guest" ? "web" : "api");
    if (entry.userId === "guest") {
      return {
        id: `usage_guest_${randomUUID()}`,
        userId: entry.userId,
        endpoint: entry.endpoint,
        model: entry.model,
        credits: entry.credits,
        channel,
        apiKeyId: entry.apiKeyId,
        createdAt: new Date().toISOString(),
      };
    }

    const id = `usage_${randomUUID()}`;
    const { data, error } = await this.supabase.from("usage_events").insert({
      id,
      user_id: entry.userId,
      endpoint: entry.endpoint,
      model: entry.model,
      credits: entry.credits,
      channel,
      api_key_id: entry.apiKeyId ?? null,
    }).select("created_at").single();
    if (error) {
      throw new Error(`failed to record usage: ${error.message}`);
    }
    return {
      id,
      userId: entry.userId,
      endpoint: entry.endpoint,
      model: entry.model,
      credits: entry.credits,
      channel,
      apiKeyId: entry.apiKeyId,
      createdAt: new Date(data.created_at as string | Date).toISOString(),
    };
  }

  private mapUsageRow(row: Record<string, unknown>): UsageEvent {
    return {
      id: String(row.id),
      userId: String(row.user_id),
      endpoint: String(row.endpoint),
      model: String(row.model),
      credits: Number(row.credits),
      channel: String(row.channel ?? "api") as UsageChannel,
      apiKeyId: row.api_key_id ? String(row.api_key_id) : undefined,
      createdAt: new Date(row.created_at as string | Date).toISOString(),
    };
  }

  async list(userId: string): Promise<UsageEvent[]> {
    const { data, error } = await this.supabase
      .from("usage_events")
      .select("id, user_id, endpoint, model, credits, channel, api_key_id, created_at")
      .eq("user_id", userId)
      .order("created_at", { ascending: false })
      .limit(500);
    if (error) {
      throw new Error(`failed to read usage events: ${error.message}`);
    }
    return (data ?? []).map((row) => this.mapUsageRow(row));
  }

  async listRecent(userId: string, windowDays: number): Promise<UsageEvent[]> {
    const { data, error } = await this.supabase
      .from("usage_events")
      .select("id, user_id, endpoint, model, credits, channel, api_key_id, created_at")
      .eq("user_id", userId)
      .gte("created_at", this.buildWindowStart(windowDays))
      .order("created_at", { ascending: false });
    if (error) {
      throw new Error(`failed to read recent usage events: ${error.message}`);
    }
    return (data ?? []).map((row) => this.mapUsageRow(row));
  }

  async listWithSummary(userId: string, windowDays = 7): Promise<{ data: UsageEvent[]; summary: UsageSummary }> {
    const [data, summaryRows] = await Promise.all([
      this.list(userId),
      this.listRecent(userId, windowDays),
    ]);
    return {
      data,
      summary: this.buildSummary(summaryRows, windowDays),
    };
  }

  async trafficAnalytics(windowDays = 7): Promise<TrafficAnalyticsSnapshot> {
    const windowStart = this.buildWindowStart(windowDays);
    const [{ data: usageRows, error: usageError }, { data: guestUsageRows, error: guestUsageError }, {
      data: guestSessions,
      error: guestSessionsError,
    }] = await Promise.all([
      this.supabase
        .from("usage_events")
        .select("credits, channel, api_key_id, created_at")
        .gte("created_at", windowStart),
      this.supabase
        .from("guest_usage_events")
        .select("credits, created_at")
        .gte("created_at", windowStart),
      this.supabase
        .from("guest_sessions")
        .select("guest_id")
        .gte("created_at", windowStart),
    ]);

    if (usageError) {
      throw new Error(`failed to read traffic analytics usage events: ${usageError.message}`);
    }
    if (guestUsageError) {
      throw new Error(`failed to read guest traffic analytics: ${guestUsageError.message}`);
    }
    if (guestSessionsError) {
      throw new Error(`failed to read guest sessions analytics: ${guestSessionsError.message}`);
    }

    const api = { requests: 0, credits: 0 };
    const web = { requests: 0, credits: 0 };
    let authenticatedWebRequests = 0;
    const byApiKey = new Map<string, UsageSummaryBucket>();

    for (const row of usageRows ?? []) {
      const channel = String(row.channel ?? "api") as UsageChannel;
      const credits = Number(row.credits ?? 0);
      const target = channel === "web" ? web : api;
      target.requests += 1;
      target.credits += credits;
      if (channel === "web") {
        authenticatedWebRequests += 1;
      }

      const apiKeyId = row.api_key_id ? String(row.api_key_id) : "";
      if (channel === "api" && apiKeyId) {
        const bucket = byApiKey.get(apiKeyId) ?? { key: apiKeyId, requests: 0, credits: 0 };
        bucket.requests += 1;
        bucket.credits += credits;
        byApiKey.set(apiKeyId, bucket);
      }
    }

    let guestRequests = 0;
    for (const row of guestUsageRows ?? []) {
      guestRequests += 1;
      web.requests += 1;
      web.credits += Number(row.credits ?? 0);
    }

    const guestSessionIds = [...new Set((guestSessions ?? []).map((row) => String(row.guest_id)))];
    const guestSessionCount = guestSessionIds.length;
    const linkedGuests = guestSessionCount > 0
      ? (await this.listLinkedGuestIds(guestSessionIds)).size
      : 0;

    return {
      windowDays,
      channels: { api, web },
      byApiKey: [...byApiKey.values()].sort((left, right) => {
        if (right.credits !== left.credits) {
          return right.credits - left.credits;
        }
        return left.key.localeCompare(right.key);
      }),
      webBreakdown: {
        guestRequests,
        authenticatedRequests: authenticatedWebRequests,
        guestSessions: guestSessionCount,
        linkedGuests,
        conversionRate: guestSessionCount > 0 ? linkedGuests / guestSessionCount : 0,
      },
    };
  }
}

class PersistentPaymentService {
  constructor(
    private readonly store: BillingStore,
    private readonly credits: PersistentCreditService,
  ) { }

  async createIntent(input: { userId: string; provider: "bkash" | "sslcommerz"; bdtAmount: number }) {
    const intent: PersistentPaymentIntent = {
      intentId: `intent_${Math.random().toString(36).slice(2, 12)}`,
      userId: input.userId,
      provider: input.provider,
      bdtAmount: Math.max(0, input.bdtAmount),
      status: "initiated",
      mintedCredits: 0,
    };
    await this.store.createPaymentIntent(intent);
    return intent;
  }

  getIntent(intentId: string) {
    return this.store.getPaymentIntent(intentId);
  }

  async applyWebhook(payload: {
    provider: "bkash" | "sslcommerz";
    intent_id: string;
    provider_txn_id: string;
    verified: boolean;
  }) {
    if (!payload.verified) {
      await this.store.markPaymentCredited(payload.intent_id, 0, "failed");
      return this.store.getPaymentIntent(payload.intent_id);
    }

    // Call the PostgreSQL RPC for atomic idempotency, intent verification, and crediting
    const result = await this.store.claimPaymentIntent(payload.intent_id, payload.provider, payload.provider_txn_id);
    if (!result.success || !result.intent) {
      throw new Error(
        `payment intent claim failed: ${result.error ?? "unknown error"} ` +
        `(intent_id=${payload.intent_id}, provider=${payload.provider}, provider_txn_id=${payload.provider_txn_id})`,
      );
    }

    return result.intent;
  }

  async confirmDemoIntent(input: { intentId: string; providerTxnId?: string }) {
    return this.applyWebhook({
      provider: "bkash",
      intent_id: input.intentId,
      provider_txn_id: input.providerTxnId ?? `demo_txn_${randomUUID().slice(0, 10)}`,
      verified: true,
    });
  }
}

export class PersistentUserService {
  constructor(
    private readonly apiKeys: ApiKeyStore,
    private readonly supabaseUsers: SupabaseUserStore,
    private readonly guests: GuestAttributionStore,
  ) { }

  async me(userId: string) {
    const user = await this.supabaseUsers.findById(userId);
    if (!user) {
      return undefined;
    }
    const keys = await this.apiKeys.list(userId);
    const events = await this.apiKeys.listEvents(userId);
    return {
      userId: user.userId,
      email: user.email,
      name: user.name,
      createdAt: user.createdAt,
      apiKeys: keys.map((key) => ({
        id: key.id,
        key_id: key.keyPrefix,
        nickname: key.nickname,
        status: key.status,
        revoked: key.revoked,
        scopes: key.scopes,
        createdAt: key.createdAt,
        expiresAt: key.expiresAt,
        revokedAt: key.revokedAt,
      })),
      apiKeyEvents: events,
    };
  }

  async validateApiKey(key: string, requiredScope: string): Promise<string | null> {
    const apiKey = await this.apiKeys.resolve(key);
    if (!apiKey || !apiKey.scopes.includes(requiredScope)) {
      return null;
    }
    return apiKey.userId;
  }

  async resolveApiKey(key: string): Promise<{ userId: string; scopes: string[]; apiKeyId?: string } | null> {
    return this.apiKeys.resolve(key);
  }
  async createApiKey(userId: string, input: {
    nickname: string;
    scopes: string[];
    expiresAt?: string;
  }) {
    const key = createApiKey();
    await this.apiKeys.create({
      key,
      userId,
      nickname: input.nickname,
      scopes: input.scopes,
      expiresAt: input.expiresAt,
    });
    return {
      key,
      nickname: input.nickname,
      scopes: input.scopes,
      expiresAt: input.expiresAt,
    };
  }

  revokeApiKey(userId: string, id: string): Promise<boolean> {
    return this.apiKeys.revokeById(id, userId);
  }

  async ensureSessionUser(sessionUser: SessionUserIdentity): Promise<void> {
    const normalizedEmail = sessionUser.email.trim().toLowerCase();
    const normalizedName = typeof sessionUser.name === "string" && sessionUser.name.trim().length > 0
      ? sessionUser.name.trim()
      : undefined;
    const existingUser = await this.supabaseUsers.findById(sessionUser.userId);
    const nextName = normalizedName ?? existingUser?.name;

    if (
      existingUser
      && existingUser.email.toLowerCase() === normalizedEmail
      && (normalizedName === undefined || (existingUser.name ?? undefined) === normalizedName)
    ) {
      return;
    }

    await this.supabaseUsers.upsertProfile({
      userId: sessionUser.userId,
      email: normalizedEmail,
      name: nextName,
    });
  }

  linkGuest(guestId: string, userId: string, linkSource = "auth_session"): Promise<void> {
    return this.guests.linkGuestToUser({ guestId, userId, linkSource });
  }
}



class RuntimeAiService {
  constructor(
    private readonly models: ModelService,
    private readonly credits: PersistentCreditService,
    private readonly usage: PersistentUsageService,
    private readonly guests: GuestAttributionStore,
    private readonly providerRegistry: ProviderRegistry,
    private readonly langfuse: LangfuseClient,
  ) { }

  private buildUsageContext(context: { channel: UsageChannel; apiKeyId?: string }) {
    return {
      channel: context.channel,
      apiKeyId: context.apiKeyId,
    };
  }

  private async refundOnUsageFailure(
    userId: string,
    creditsCost: number,
    chargeReferenceId: string,
    operation: () => Promise<void>,
  ) {
    if (creditsCost <= 0) {
      await operation();
      return;
    }

    try {
      await operation();
    } catch (error) {
      await this.credits.refund(userId, creditsCost, `refund_${chargeReferenceId}`);
      throw error;
    }
  }

  async chatCompletions(
    userId: string,
    modelId: string | undefined,
    messages: ChatMessage[],
    usageContext: { channel: UsageChannel; apiKeyId?: string },
  ) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const model = modelId && modelId !== "auto" ? this.models.findById(modelId) : this.models.pickDefault("chat");
    if (!model || model.capability !== "chat") {
      return { error: "unknown model", statusCode: 400 as const };
    }
    const creditsCost = this.models.creditsForRequest(model);
    const chargeReferenceId = `req_${randomUUID()}`;
    const consumed = creditsCost > 0 ? await this.credits.consume(userId, creditsCost, chargeReferenceId) : true;
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }

    const text = messages.map((msg) => msg.content).join(" ").trim();
    let providerResult;
    try {
      providerResult = await this.providerRegistry.chat(
        model.id,
        messages.map((message) => ({
          role: this.normalizeRole(message.role),
          content: message.content,
        })),
      );
    } catch (error) {
      if (creditsCost > 0) {
        await this.credits.refund(userId, creditsCost, `refund_${chargeReferenceId}`);
      }
      return {
        error: error instanceof Error ? error.message : "provider unavailable",
        statusCode: 502 as const,
      };
    }

    await this.refundOnUsageFailure(userId, creditsCost, chargeReferenceId, async () => {
      await this.usage.add({
        userId,
        endpoint: "/v1/chat/completions",
        model: model.id,
        credits: creditsCost,
        ...resolvedUsageContext,
      });
    });

    await this.langfuse.trace({
      userId,
      model: model.id,
      provider: providerResult.providerUsed,
      endpoint: "/v1/chat/completions",
      credits: creditsCost,
      promptPreview: text.slice(0, 160),
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
              content: providerResult.content || `MVP response: ${text || "Your request was processed."}`,
            },
          },
        ],
      },
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": providerResult.providerUsed,
        "x-provider-model": providerResult.providerModel,
        "x-actual-credits": String(creditsCost),
      },
    };
  }

  async responses(userId: string, input: string, usageContext: { channel: UsageChannel; apiKeyId?: string }) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const model = this.models.pickDefault("chat");
    const creditsCost = Math.max(4, Math.floor(this.models.creditsForRequest(model) * 0.75));
    const chargeReferenceId = `req_${randomUUID()}`;
    const consumed = await this.credits.consume(userId, creditsCost, chargeReferenceId);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }

    let providerResult;
    try {
      providerResult = await this.providerRegistry.chat(model.id, [{ role: "user", content: input }]);
    } catch (error) {
      await this.credits.refund(userId, creditsCost, `refund_${chargeReferenceId}`);
      return {
        error: error instanceof Error ? error.message : "provider unavailable",
        statusCode: 502 as const,
      };
    }

    await this.refundOnUsageFailure(userId, creditsCost, chargeReferenceId, async () => {
      await this.usage.add({
        userId,
        endpoint: "/v1/responses",
        model: model.id,
        credits: creditsCost,
        ...resolvedUsageContext,
      });
    });

    return {
      statusCode: 200 as const,
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": providerResult.providerUsed,
        "x-provider-model": providerResult.providerModel,
        "x-actual-credits": String(creditsCost),
      },
      body: {
        id: `resp_${randomUUID().slice(0, 12)}`,
        object: "response",
        model: model.id,
        output: [{ type: "text", text: providerResult.content || `MVP output: ${input || "No input provided."}` }],
      },
    };
  }

  async imageGeneration(
    userId: string,
    request: ImageGenerationRequest,
    usageContext: { channel: UsageChannel; apiKeyId?: string },
  ) {
    const resolvedUsageContext = this.buildUsageContext(usageContext);
    const model = request.model && request.model !== "auto" ? this.models.findById(request.model) : this.models.pickDefault("image");
    if (!model || model.capability !== "image") {
      return { error: "unknown model", statusCode: 400 as const };
    }
    const creditsCost = this.models.creditsForRequest(model);
    const chargeReferenceId = `req_${randomUUID()}`;
    const consumed = await this.credits.consume(userId, creditsCost, chargeReferenceId);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }

    let providerResult;
    try {
      providerResult = await this.providerRegistry.imageGeneration(model.id, {
        prompt: request.prompt,
        n: request.n ?? 1,
        size: request.size,
        responseFormat: request.responseFormat ?? "url",
        user: request.user,
      });
    } catch {
      await this.credits.refund(userId, creditsCost, `refund_${chargeReferenceId}`);
      return {
        error: "provider unavailable",
        statusCode: 502 as const,
        headers: {
          "x-model-routed": model.id,
        },
      };
    }

    await this.refundOnUsageFailure(userId, creditsCost, chargeReferenceId, async () => {
      await this.usage.add({
        userId,
        endpoint: "/v1/images/generations",
        model: model.id,
        credits: creditsCost,
        ...resolvedUsageContext,
      });
    });
    return {
      statusCode: 200 as const,
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": providerResult.providerUsed,
        "x-provider-model": providerResult.providerModel,
        "x-actual-credits": String(creditsCost),
      },
      body: {
        created: providerResult.created,
        data: providerResult.data.map((entry) => ({
          ...(entry.url ? { url: entry.url } : {}),
          ...(entry.b64Json ? { b64_json: entry.b64Json } : {}),
        })),
      },
    };
  }

  async guestChatCompletions(
    guestId: string,
    modelId: string | undefined,
    messages: ChatMessage[],
    guestIp?: string,
  ) {
    const model = modelId && modelId !== "auto" ? this.models.findById(modelId) : this.models.pickGuestDefault("chat");
    if (!model || model.capability !== "chat") {
      return { error: "unknown model", statusCode: 400 as const };
    }
    if (model.costType !== "free") {
      return { error: "forbidden", statusCode: 403 as const };
    }

    const text = messages.map((msg) => msg.content).join(" ").trim();

    let providerResult;
    try {
      providerResult = await this.providerRegistry.chat(
        model.id,
        messages.map((message) => ({
          role: this.normalizeRole(message.role),
          content: message.content,
        })),
      );
    } catch (error) {
      return {
        error: error instanceof Error ? error.message : "provider unavailable",
        statusCode: 502 as const,
      };
    }

    await this.guests.addUsage({
      guestId,
      endpoint: "/v1/web/chat/guest",
      model: model.id,
      credits: 0,
      ipAddress: guestIp,
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
              content: providerResult.content || `MVP response: ${text || "Your request was processed."}`,
            },
          },
        ],
      },
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": providerResult.providerUsed,
        "x-provider-model": providerResult.providerModel,
        "x-actual-credits": "0",
      },
    };
  }

  providersStatus() {
    return this.providerRegistry.status();
  }

  providersMetrics() {
    return this.providerRegistry.metrics();
  }

  providersMetricsPrometheus() {
    return this.providerRegistry.metricsPrometheus();
  }

  private normalizeRole(role: string): "system" | "user" | "assistant" {
    if (role === "system" || role === "assistant") {
      return role;
    }
    return "user";
  }
}

export type RuntimeServices = {
  env: AppEnv;
  models: ModelService;
  credits: PersistentCreditService;
  usage: PersistentUsageService;
  payments: PersistentPaymentService;
  reconciliation: PaymentReconciliationService;
  reconciliationScheduler?: PaymentReconciliationScheduler;
  users: PersistentUserService;
  guests: GuestAttributionStore;
  authz: AuthorizationService;
  userSettings: UserSettingsService;
  supabaseAuth: SupabaseAuthService;
  ai: RuntimeAiService;
  rateLimiter: RedisRateLimiter;
  adapters: {
    bkash: BkashAdapter;
    sslcommerz: SslcommerzAdapter;
  };
};

function shouldWarnForStartupReadiness(result: ProviderReadinessStatus): boolean {
  return !result.ready && result.detail !== "disabled by config" && result.detail !== "not registered";
}

function startProviderReadinessCapture(providerRegistry: ProviderRegistry): void {
  void providerRegistry
    .captureStartupReadiness()
    .then((results) => {
      for (const [provider, result] of Object.entries(results) as Array<[ProviderName, ProviderReadinessStatus]>) {
        if (shouldWarnForStartupReadiness(result)) {
          console.warn(
            {
              provider,
              detail: result.detail,
            },
            "provider startup readiness warning",
          );
        }
      }
    })
    .catch((error) => {
      const reason = error instanceof Error ? error.message : String(error);
      console.error({ error: reason }, "provider startup readiness failed");
    });
}

export function createRuntimeServices(): RuntimeServices {
  const env = getEnv();
  const supabase = createSupabaseAdminClient(env);
  const billingStore: BillingStore = new SupabaseBillingStore(supabase);
  const credits = new PersistentCreditService(billingStore);
  const usage = new PersistentUsageService(supabase);
  const guests = new SupabaseGuestAttributionStore(supabase);
  const payments = new PersistentPaymentService(billingStore, credits);
  const reconciliation = new PaymentReconciliationService(billingStore);
  const reconciliationScheduler = env.paymentReconciliation.enabled
    ? new PaymentReconciliationScheduler(
      reconciliation,
      {
        warn: (payload, message) => console.warn(message ?? "payment reconciliation warning", payload),
        error: (payload, message) => console.error(message ?? "payment reconciliation error", payload),
      },
      {
        intervalMs: env.paymentReconciliation.intervalMs,
        lookbackHours: env.paymentReconciliation.lookbackHours,
      },
    )
    : undefined;
  reconciliationScheduler?.start();
  const apiKeyStore: ApiKeyStore = new SupabaseApiKeyStore(supabase);
  const supabaseUserStore = new SupabaseUserStore(supabase);
  const users = new PersistentUserService(apiKeyStore, supabaseUserStore, guests);
  const authz = new AuthorizationService(supabase);
  const userSettings = new UserSettingsService(supabaseUserStore);
  const supabaseAuth = new SupabaseAuthService(supabase);
  const langfuse = new LangfuseClient({
    enabled: env.langfuse.enabled,
    baseUrl: env.langfuse.baseUrl,
    publicKey: env.langfuse.publicKey,
    secretKey: env.langfuse.secretKey,
  });
  const openaiConfig = env.providers.openai ?? {
    baseUrl: "https://api.openai.com/v1",
    apiKey: undefined,
    chatModel: "gpt-4o-mini",
    imageModel: "gpt-image-1",
    freeModel: undefined,
    timeoutMs: 4000,
    maxRetries: 1,
  };
  const openrouterConfig = env.providers.openrouter ?? {
    baseUrl: "https://openrouter.ai/api/v1",
    apiKey: undefined,
    model: "openrouter/auto",
    freeModel: undefined,
    timeoutMs: 4000,
    maxRetries: 1,
  };
  const geminiConfig = env.providers.gemini ?? {
    baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai/",
    apiKey: undefined,
    model: "gemini-3-flash-preview",
    freeModel: undefined,
    timeoutMs: 4000,
    maxRetries: 1,
  };
  const anthropicConfig = env.providers.anthropic ?? {
    baseUrl: "https://api.anthropic.com/v1",
    apiKey: undefined,
    model: "claude-sonnet-4-20250514",
    freeModel: undefined,
    timeoutMs: 4000,
    maxRetries: 1,
  };
  const providerOffers = buildProviderOfferCatalog(env);
  const enabledFreeModelIds = Object.entries(providerOffers.modelOffers)
    .filter(([, offers]) => offers.length > 0)
    .map(([modelId]) => modelId);
  const models = new ModelService({ enabledFreeModelIds });

  const providerModelMap: Record<ProviderName, string> = {
    mock: "mock-chat",
    ollama: env.providers.ollama.model,
    groq: env.providers.groq.model,
    openai: "imageModel" in openaiConfig ? openaiConfig.imageModel : (openaiConfig as { model?: string }).model ?? "gpt-image-1",
    openrouter: openrouterConfig.model,
    gemini: geminiConfig.model,
    anthropic: anthropicConfig.model,
  };
  const providerReadinessModels: Record<ProviderName, string[]> = {
    mock: ["mock-chat"],
    ollama: [env.providers.ollama.model, env.providers.ollama.freeModel].filter((value): value is string => Boolean(value)),
    groq: [env.providers.groq.model, env.providers.groq.freeModel].filter((value): value is string => Boolean(value)),
    openai: [openaiConfig.chatModel, openaiConfig.imageModel, openaiConfig.freeModel].filter((value): value is string => Boolean(value)),
    openrouter: [openrouterConfig.model, openrouterConfig.freeModel].filter((value): value is string => Boolean(value)),
    gemini: [geminiConfig.model, geminiConfig.freeModel].filter((value): value is string => Boolean(value)),
    anthropic: [anthropicConfig.model, anthropicConfig.freeModel].filter((value): value is string => Boolean(value)),
  };
  const providerRegistry = new ProviderRegistry({
    clients: [
      new OllamaProviderClient({
        baseUrl: env.providers.ollama.baseUrl,
        timeoutMs: env.providers.ollama.timeoutMs,
        maxRetries: env.providers.ollama.maxRetries,
      }),
      new GroqProviderClient({
        baseUrl: env.providers.groq.baseUrl,
        apiKey: env.providers.groq.apiKey,
        timeoutMs: env.providers.groq.timeoutMs,
        maxRetries: env.providers.groq.maxRetries,
      }),
      new OpenRouterProviderClient({
        baseUrl: openrouterConfig.baseUrl,
        apiKey: openrouterConfig.apiKey,
        timeoutMs: openrouterConfig.timeoutMs,
        maxRetries: openrouterConfig.maxRetries,
      }),
      new OpenAIProviderClient({
        baseUrl: openaiConfig.baseUrl,
        apiKey: openaiConfig.apiKey,
        timeoutMs: openaiConfig.timeoutMs,
        maxRetries: openaiConfig.maxRetries,
      }),
      new GeminiProviderClient({
        baseUrl: geminiConfig.baseUrl,
        apiKey: geminiConfig.apiKey,
        timeoutMs: geminiConfig.timeoutMs,
        maxRetries: geminiConfig.maxRetries,
      }),
      new AnthropicProviderClient({
        baseUrl: anthropicConfig.baseUrl,
        apiKey: anthropicConfig.apiKey,
        timeoutMs: anthropicConfig.timeoutMs,
        maxRetries: anthropicConfig.maxRetries,
      }),
      new MockProviderClient(),
    ],
    defaultProvider: "mock",
    modelProviderMap: {
      "fast-chat": "ollama",
      "smart-reasoning": "groq",
      "image-basic": "openai",
    },
    providerModelMap,
    providerReadinessModels,
    offerCatalog: providerOffers.offers,
    modelOfferMap: providerOffers.modelOffers,
    modelOfferPolicyMap: {
      "guest-free": {
        allowedCostClasses: ["zero"],
      },
    },
    fallbackOrder: {
      ollama: ["groq", "mock"],
      groq: ["ollama", "mock"],
      openrouter: ["mock"],
      gemini: ["mock"],
      anthropic: ["mock"],
      openai: ["mock"],
      mock: [],
    },
    circuitBreaker: env.providers.circuitBreaker,
  });
  startProviderReadinessCapture(providerRegistry);

  const ai = new RuntimeAiService(models, credits, usage, guests, providerRegistry, langfuse);

  return {
    env,
    models,
    credits,
    usage,
    payments,
    reconciliation,
    reconciliationScheduler,
    users,
    guests,
    authz,
    userSettings,
    supabaseAuth,
    ai,
    rateLimiter: new RedisRateLimiter(env.redisUrl, env.rateLimitPerMinute),
    adapters: {
      bkash: new BkashAdapter({
        webhookSecret: env.webhookSecrets.bkash,
        verifyEndpoint: env.bkash.verifyEndpoint,
        bearerToken: env.bkash.bearerToken,
      }),
      sslcommerz: new SslcommerzAdapter({
        webhookSecret: env.webhookSecrets.sslcommerz,
        validatorEndpoint: env.sslcommerz.validatorEndpoint,
        storeId: env.sslcommerz.storeId,
        storePassword: env.sslcommerz.storePassword,
      }),
    },
  };
}
