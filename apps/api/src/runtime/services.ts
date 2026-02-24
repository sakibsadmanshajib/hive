import { randomUUID } from "node:crypto";
import type { CreditBalance, UsageEvent } from "../domain/types";
import { ModelService } from "../domain/model-service";
import { getEnv, type AppEnv } from "../config/env";
import { BkashAdapter, SslcommerzAdapter } from "./provider-adapters";
import { GroqProviderClient } from "../providers/groq-client";
import { MockProviderClient } from "../providers/mock-client";
import { OllamaProviderClient } from "../providers/ollama-client";
import { ProviderRegistry } from "../providers/registry";
import type { ProviderName } from "../providers/types";
import { PostgresStore, type PersistentApiKey, type PersistentPaymentIntent } from "./postgres-store";
import { RedisRateLimiter } from "./redis-rate-limiter";
import { createApiKey, hashPassword, verifyPassword } from "./security";
import { LangfuseClient } from "./langfuse";
import { AuthorizationService } from "./authorization";
import { UserSettingsService } from "./user-settings";
import { createSupabaseAdminClient } from "./supabase-client";
import { SupabaseAuthService } from "./supabase-auth-service";
import { SupabaseApiKeyStore } from "./supabase-api-key-store";
import { SupabaseUserStore } from "./supabase-user-store";

type ChatMessage = { role: string; content: string };

type ApiKeyStore = {
  create(input: { key: string; userId: string; scopes: string[] }): Promise<void>;
  resolve(key: string): Promise<{ userId: string; scopes: string[] } | null>;
  list(userId: string): Promise<PersistentApiKey[]>;
  revoke(key: string, userId: string): Promise<boolean>;
  get(key: string): Promise<PersistentApiKey | undefined>;
};

class PostgresApiKeyStore implements ApiKeyStore {
  constructor(private readonly store: PostgresStore) {}

  create(input: { key: string; userId: string; scopes: string[] }): Promise<void> {
    return this.store.createApiKey(input);
  }

  async resolve(key: string): Promise<{ userId: string; scopes: string[] } | null> {
    const apiKey = await this.store.getApiKey(key);
    if (!apiKey || apiKey.revoked) {
      return null;
    }
    return { userId: apiKey.userId, scopes: apiKey.scopes };
  }

  list(userId: string): Promise<PersistentApiKey[]> {
    return this.store.listApiKeys(userId);
  }

  revoke(key: string, userId: string): Promise<boolean> {
    return this.store.revokeApiKey(key, userId);
  }

  get(key: string): Promise<PersistentApiKey | undefined> {
    return this.store.getApiKey(key);
  }
}

class PersistentCreditService {
  constructor(private readonly store: PostgresStore) {}

  getBalance(userId: string): Promise<CreditBalance> {
    return this.store.getBalance(userId);
  }

  consume(userId: string, credits: number): Promise<boolean> {
    return this.store.consumeCredits(userId, credits, `req_${randomUUID()}`);
  }

  topUp(userId: string, bdtAmount: number, referenceId: string): Promise<CreditBalance> {
    return this.store.topUp(userId, bdtAmount, referenceId);
  }
}

class PersistentUsageService {
  constructor(private readonly store: PostgresStore) {}

  add(entry: Omit<UsageEvent, "id" | "createdAt">): Promise<UsageEvent> {
    return this.store.addUsage(entry.userId, entry.endpoint, entry.model, entry.credits);
  }

  list(userId: string): Promise<UsageEvent[]> {
    return this.store.listUsage(userId);
  }
}

class PersistentPaymentService {
  constructor(
    private readonly store: PostgresStore,
    private readonly credits: PersistentCreditService,
  ) {}

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
    const eventKey = `${payload.provider}:${payload.provider_txn_id}`;
    const inserted = await this.store.recordPaymentEvent(
      eventKey,
      payload.intent_id,
      payload.provider,
      payload.provider_txn_id,
      payload.verified,
    );
    if (!inserted) {
      return this.store.getPaymentIntent(payload.intent_id);
    }

    const intent = await this.store.getPaymentIntent(payload.intent_id);
    if (!intent) {
      return undefined;
    }

    if (intent.provider !== payload.provider) {
      await this.store.markPaymentCredited(intent.intentId, 0, "failed");
      return this.store.getPaymentIntent(payload.intent_id);
    }

    if (!payload.verified) {
      await this.store.markPaymentCredited(intent.intentId, 0, "failed");
      return this.store.getPaymentIntent(payload.intent_id);
    }

    if (intent.status === "credited") {
      return intent;
    }

    const mintedCredits = Math.trunc(intent.bdtAmount * 100);
    await this.credits.topUp(intent.userId, intent.bdtAmount, `payment_${intent.intentId}`);
    await this.store.markPaymentCredited(intent.intentId, mintedCredits, "credited");
    return this.store.getPaymentIntent(payload.intent_id);
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

class PersistentUserService {
  constructor(
    private readonly store: PostgresStore,
    private readonly apiKeys: ApiKeyStore,
    private readonly supabaseUsers?: SupabaseUserStore,
  ) {}

  async register(input: { email: string; password: string; name?: string }) {
    const existing = await this.store.findUserByEmail(input.email);
    if (existing) {
      return { error: "email already registered" as const };
    }

    const userId = randomUUID();
    await this.store.createUser({
      userId,
      email: input.email,
      name: input.name,
      passwordHash: hashPassword(input.password),
    });
    const apiKey = createApiKey();
    await this.apiKeys.create({ key: apiKey, userId, scopes: ["chat", "image", "usage", "billing"] });
    return {
      userId,
      email: input.email,
      name: input.name,
      apiKey,
    };
  }

  async login(input: { email: string; password: string }) {
    const user = await this.store.findUserByEmail(input.email);
    if (!user || !verifyPassword(input.password, user.passwordHash)) {
      return { error: "invalid credentials" as const };
    }
    const apiKey = createApiKey();
    await this.apiKeys.create({ key: apiKey, userId: user.userId, scopes: ["chat", "image", "usage", "billing"] });
    return {
      userId: user.userId,
      email: user.email,
      name: user.name,
      apiKey,
    };
  }

  async validateApiKey(key: string, requiredScope: string): Promise<string | null> {
    const apiKey = await this.apiKeys.resolve(key);
    if (!apiKey || !apiKey.scopes.includes(requiredScope)) {
      return null;
    }
    return apiKey.userId;
  }

  async resolveApiKey(key: string): Promise<{ userId: string; scopes: string[] } | null> {
    return this.apiKeys.resolve(key);
  }

  async me(userId: string) {
    const user = this.supabaseUsers ? await this.supabaseUsers.findById(userId) : await this.store.findUserById(userId);
    if (!user) {
      return undefined;
    }
    const keys = await this.apiKeys.list(userId);
    return {
      userId: user.userId,
      email: user.email,
      name: user.name,
      createdAt: user.createdAt,
      apiKeys: keys.map((key) => ({
        key_id: key.key.slice(-8),
        revoked: key.revoked,
        scopes: key.scopes,
        createdAt: key.createdAt,
      })),
    };
  }

  async createApiKey(userId: string, scopes: string[]) {
    const key = createApiKey();
    await this.apiKeys.create({ key, userId, scopes });
    return key;
  }

  revokeApiKey(userId: string, key: string): Promise<boolean> {
    return this.apiKeys.revoke(key, userId);
  }
}

class RuntimeAuthService {
  constructor(
    private readonly store: PostgresStore,
    private readonly users: PersistentUserService,
    private readonly settings: UserSettingsService,
    private readonly env: AppEnv,
  ) {}

  async startGoogleAuth(): Promise<{ authorizationUrl: string; state: string }> {
    const state = randomUUID().replace(/-/g, "").slice(0, 24);
    const expiresAt = new Date(Date.now() + 10 * 60 * 1000);
    await this.store.createOAuthState(state, expiresAt);

    const params = new URLSearchParams({
      client_id: this.env.google.clientId,
      redirect_uri: this.env.google.redirectUri,
      response_type: "code",
      scope: "openid email profile",
      state,
    });

    return {
      authorizationUrl: `https://accounts.google.com/o/oauth2/v2/auth?${params.toString()}`,
      state,
    };
  }

  async completeGoogleAuth(input: {
    state?: string;
    code?: string;
  }): Promise<{ error?: string; sessionToken?: string; userId?: string; email?: string; name?: string }> {
    if (!input.state || !input.code) {
      return { error: "state and code are required" };
    }
    const stateOk = await this.store.consumeOAuthState(input.state);
    if (!stateOk) {
      return { error: "invalid oauth state" };
    }

    const googleIdentity = {
      email: `google_${input.code.slice(0, 10)}@example.invalid`,
      name: "Google User",
      subject: `google_sub_${input.code.slice(0, 12)}`,
    };

    const existing = await this.store.findUserByEmail(googleIdentity.email);
    const userId = existing?.userId ?? randomUUID();
    if (!existing) {
      await this.store.createUser({
        userId,
        email: googleIdentity.email,
        name: googleIdentity.name,
        passwordHash: hashPassword(randomUUID()),
      });
      await this.settings.updateForUser(userId, {});
    }

    const sessionToken = `sess_${randomUUID().replace(/-/g, "")}`;
    const expiresAt = new Date(Date.now() + this.env.auth.sessionTtlMinutes * 60 * 1000);
    await this.store.createAuthSession({
      token: sessionToken,
      userId,
      provider: "google",
      providerSubject: googleIdentity.subject,
      providerEmail: googleIdentity.email,
      expiresAt,
    });

    return {
      sessionToken,
      userId,
      email: googleIdentity.email,
      name: googleIdentity.name,
    };
  }

  async getSessionPrincipal(sessionToken: string): Promise<{ userId: string } | null> {
    const session = await this.store.findAuthSessionByToken(sessionToken);
    if (!session || session.revoked) {
      return null;
    }
    if (new Date(session.expiresAt).getTime() <= Date.now()) {
      return null;
    }
    return { userId: session.userId };
  }

  revokeSession(sessionToken: string): Promise<boolean> {
    return this.store.revokeAuthSession(sessionToken);
  }
}

class TwoFactorService {
  constructor(private readonly store: PostgresStore) {}

  async initEnrollment(userId: string): Promise<{ challengeId: string; secret: string; method: string }> {
    const existing = await this.store.getUserTwoFactor(userId);
    const secret = existing?.secret ?? randomUUID().replace(/-/g, "").slice(0, 16);
    await this.store.upsertUserTwoFactor({ userId, secret, enabled: Boolean(existing?.enabled) });

    const challengeId = `chlg_${randomUUID().slice(0, 12)}`;
    await this.store.createTwoFactorChallenge({
      challengeId,
      userId,
      purpose: "enroll",
      challengeCode: "000000",
      expiresAt: new Date(Date.now() + 10 * 60 * 1000),
    });

    return { challengeId, secret, method: "totp" };
  }

  async verifyEnrollment(userId: string, challengeId: string, code: string): Promise<boolean> {
    const ok = await this.store.verifyTwoFactorChallenge(challengeId, code);
    if (!ok) {
      return false;
    }
    const current = await this.store.getUserTwoFactor(userId);
    if (!current) {
      return false;
    }
    await this.store.upsertUserTwoFactor({ userId, secret: current.secret, enabled: true });
    return true;
  }

  async initChallenge(userId: string, purpose: string): Promise<{ challengeId: string; expiresInSeconds: number }> {
    const challengeId = `chlg_${randomUUID().slice(0, 12)}`;
    await this.store.createTwoFactorChallenge({
      challengeId,
      userId,
      purpose,
      challengeCode: "000000",
      expiresAt: new Date(Date.now() + 10 * 60 * 1000),
    });
    return { challengeId, expiresInSeconds: 600 };
  }

  verifyChallenge(challengeId: string, code: string): Promise<boolean> {
    return this.store.verifyTwoFactorChallenge(challengeId, code);
  }

  hasRecentVerification(userId: string, challengeId: string, withinMinutes: number): Promise<boolean> {
    return this.store.hasVerifiedTwoFactorChallenge(userId, challengeId, withinMinutes);
  }
}

class RuntimeAiService {
  constructor(
    private readonly models: ModelService,
    private readonly credits: PersistentCreditService,
    private readonly usage: PersistentUsageService,
    private readonly providerRegistry: ProviderRegistry,
    private readonly langfuse: LangfuseClient,
  ) {}

  async chatCompletions(userId: string, modelId: string | undefined, messages: ChatMessage[]) {
    const model = modelId && modelId !== "auto" ? this.models.findById(modelId) : this.models.pickDefault("chat");
    if (!model || model.capability !== "chat") {
      return { error: "unknown model", statusCode: 400 as const };
    }
    const creditsCost = model.creditsPerRequest;
    const consumed = await this.credits.consume(userId, creditsCost);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }

    const text = messages.map((msg) => msg.content).join(" ").trim();
    await this.usage.add({ userId, endpoint: "/v1/chat/completions", model: model.id, credits: creditsCost });

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

  async responses(userId: string, input: string) {
    const model = this.models.pickDefault("chat");
    const creditsCost = Math.max(4, Math.floor(model.creditsPerRequest * 0.75));
    const consumed = await this.credits.consume(userId, creditsCost);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    await this.usage.add({ userId, endpoint: "/v1/responses", model: model.id, credits: creditsCost });

    let providerResult;
    try {
      providerResult = await this.providerRegistry.chat(model.id, [{ role: "user", content: input }]);
    } catch (error) {
      return {
        error: error instanceof Error ? error.message : "provider unavailable",
        statusCode: 502 as const,
      };
    }

    return {
      statusCode: 200 as const,
      body: {
        id: `resp_${randomUUID().slice(0, 12)}`,
        object: "response",
        model: model.id,
        output: [{ type: "text", text: providerResult.content || `MVP output: ${input || "No input provided."}` }],
      },
    };
  }

  async imageGeneration(userId: string, prompt: string) {
    const model = this.models.pickDefault("image");
    const creditsCost = model.creditsPerRequest;
    const consumed = await this.credits.consume(userId, creditsCost);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    await this.usage.add({ userId, endpoint: "/v1/images/generations", model: model.id, credits: creditsCost });
    return {
      statusCode: 200 as const,
      headers: { "x-actual-credits": String(creditsCost) },
      body: {
        created: Math.floor(Date.now() / 1000),
        object: "list",
        data: [{ url: `https://example.invalid/generated/${encodeURIComponent(prompt || "image")}.png` }],
      },
    };
  }

  providersStatus() {
    return this.providerRegistry.status();
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
  users: PersistentUserService;
  authz: AuthorizationService;
  userSettings: UserSettingsService;
  auth: RuntimeAuthService;
  supabaseAuth?: SupabaseAuthService;
  twoFactor: TwoFactorService;
  ai: RuntimeAiService;
  rateLimiter: RedisRateLimiter;
  adapters: {
    bkash: BkashAdapter;
    sslcommerz: SslcommerzAdapter;
  };
};

export function createRuntimeServices(): RuntimeServices {
  const env = getEnv();
  const store = new PostgresStore(env.postgresUrl);
  const supabase = env.supabase.flags.authEnabled || env.supabase.flags.userRepoEnabled || env.supabase.flags.apiKeysEnabled
    ? createSupabaseAdminClient(env)
    : undefined;
  const models = new ModelService();
  const credits = new PersistentCreditService(store);
  const usage = new PersistentUsageService(store);
  const payments = new PersistentPaymentService(store, credits);
  const apiKeyStore: ApiKeyStore = env.supabase.flags.apiKeysEnabled && supabase
    ? new SupabaseApiKeyStore(supabase)
    : new PostgresApiKeyStore(store);
  const supabaseUserStore = env.supabase.flags.userRepoEnabled && supabase
    ? new SupabaseUserStore(supabase)
    : undefined;
  const users = new PersistentUserService(store, apiKeyStore, supabaseUserStore);
  const authz = new AuthorizationService(store);
  const userSettings = new UserSettingsService(supabaseUserStore ?? store);
  const auth = new RuntimeAuthService(store, users, userSettings, env);
  const supabaseAuth = env.supabase.flags.authEnabled && supabase
    ? new SupabaseAuthService(supabase)
    : undefined;
  const twoFactor = new TwoFactorService(store);
  const langfuse = new LangfuseClient({
    enabled: env.langfuse.enabled,
    baseUrl: env.langfuse.baseUrl,
    publicKey: env.langfuse.publicKey,
    secretKey: env.langfuse.secretKey,
  });

  const providerModelMap: Record<ProviderName, string> = {
    mock: "mock-chat",
    ollama: env.providers.ollama.model,
    groq: env.providers.groq.model,
  };
  const providerRegistry = new ProviderRegistry({
    clients: [
      new OllamaProviderClient({ baseUrl: env.providers.ollama.baseUrl }),
      new GroqProviderClient({ baseUrl: env.providers.groq.baseUrl, apiKey: env.providers.groq.apiKey }),
      new MockProviderClient(),
    ],
    defaultProvider: "mock",
    modelProviderMap: {
      "fast-chat": "ollama",
      "smart-reasoning": "groq",
      "image-basic": "mock",
    },
    providerModelMap,
    fallbackOrder: {
      ollama: ["groq", "mock"],
      groq: ["ollama", "mock"],
      mock: [],
    },
  });

  const ai = new RuntimeAiService(models, credits, usage, providerRegistry, langfuse);

  return {
    env,
    models,
    credits,
    usage,
    payments,
    users,
    authz,
    userSettings,
    auth,
    supabaseAuth,
    twoFactor,
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
