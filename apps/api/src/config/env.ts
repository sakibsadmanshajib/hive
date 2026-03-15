import { config as loadDotEnv } from "dotenv";

loadDotEnv();

function required(name: string, fallback?: string): string {
  const value = process.env[name] ?? fallback;
  if (!value) {
    throw new Error(`Missing required environment variable: ${name}`);
  }
  return value;
}

function optional(name: string): string | undefined {
  const value = process.env[name];
  if (value === undefined) {
    return undefined;
  }
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : undefined;
}

function parseNumber(name: string, fallback: number): number {
  const raw = process.env[name];
  if (raw === undefined || raw.trim() === "") {
    return fallback;
  }
  const value = Number(raw);
  if (Number.isNaN(value)) {
    throw new Error(`Invalid numeric environment variable ${name}: ${raw}`);
  }
  return value;
}

function parsePositiveInteger(name: string, fallback: number): number {
  const value = parseNumber(name, fallback);
  if (!Number.isInteger(value) || value <= 0) {
    throw new Error(`Invalid positive integer environment variable ${name}: ${value}`);
  }
  return value;
}

function parseNonNegativeInteger(name: string, fallback: number): number {
  const value = parseNumber(name, fallback);
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`Invalid non-negative integer environment variable ${name}: ${value}`);
  }
  return value;
}

function parseBoolean(name: string, fallback: boolean): boolean {
  const raw = process.env[name];
  if (raw === undefined) {
    return fallback;
  }
  const normalized = raw.toLowerCase();
  if (normalized === "1" || normalized === "true" || normalized === "yes") {
    return true;
  }
  if (normalized === "0" || normalized === "false" || normalized === "no") {
    return false;
  }
  throw new Error(`Invalid boolean environment variable ${name}: ${raw}`);
}

export type AppEnv = {
  nodeEnv: string;
  port: number;
  postgresUrl: string;
  redisUrl: string;
  rateLimitPerMinute: number;
  adminStatusToken?: string;
  allowDemoPaymentConfirm: boolean;
  allowDevApiKeyPrefix: boolean;
  google: {
    clientId: string;
    clientSecret: string;
    redirectUri: string;
  };
  auth: {
    sessionTtlMinutes: number;
    enforceTwoFactorSensitiveActions: boolean;
    twoFactorVerificationWindowMinutes: number;
  };
  webhookSecrets: {
    bkash: string;
    sslcommerz: string;
  };
  bkash: {
    verifyEndpoint?: string;
    bearerToken?: string;
  };
  sslcommerz: {
    validatorEndpoint?: string;
    storeId?: string;
    storePassword?: string;
  };
  supabase: {
    url: string;
    serviceRoleKey: string;
    flags: {
      authEnabled: boolean;
      userRepoEnabled: boolean;
      apiKeysEnabled: boolean;
      billingStoreEnabled: boolean;
    };
  };
  paymentReconciliation: {
    enabled: boolean;
    intervalMs: number;
    lookbackHours: number;
  };
  providers: {
    circuitBreaker: {
      failureThreshold: number;
      resetTimeoutMs: number;
    };
    openrouter: {
      apiKey?: string;
      baseUrl: string;
      model: string;
      freeModel?: string;
      timeoutMs: number;
      maxRetries: number;
    };
    ollama: {
      baseUrl: string;
      model: string;
      freeModel?: string;
      timeoutMs: number;
      maxRetries: number;
    };
    groq: {
      apiKey?: string;
      baseUrl: string;
      model: string;
      freeModel?: string;
      timeoutMs: number;
      maxRetries: number;
    };
    openai: {
      apiKey?: string;
      baseUrl: string;
      chatModel: string;
      imageModel: string;
      freeModel?: string;
      timeoutMs: number;
      maxRetries: number;
    };
    gemini: {
      apiKey?: string;
      baseUrl: string;
      model: string;
      freeModel?: string;
      timeoutMs: number;
      maxRetries: number;
    };
    anthropic: {
      apiKey?: string;
      baseUrl: string;
      model: string;
      freeModel?: string;
      timeoutMs: number;
      maxRetries: number;
    };
  };
  langfuse: {
    enabled: boolean;
    baseUrl: string;
    publicKey?: string;
    secretKey?: string;
  };
};

export function getEnv(): AppEnv {
  const providerTimeoutMs = parsePositiveInteger("PROVIDER_TIMEOUT_MS", 4000);
  const providerMaxRetries = parseNonNegativeInteger("PROVIDER_MAX_RETRIES", 1);

  const env: AppEnv = {
    nodeEnv: process.env.NODE_ENV ?? "development",
    port: parseNumber("PORT", 8080),
    postgresUrl: required("POSTGRES_URL", "postgres://postgres:postgres@127.0.0.1:5432/postgres"),
    redisUrl: required("REDIS_URL", "redis://127.0.0.1:6379"),
    rateLimitPerMinute: parseNumber("RATE_LIMIT_PER_MINUTE", 60),
    adminStatusToken: optional("ADMIN_STATUS_TOKEN"),
    allowDemoPaymentConfirm: parseBoolean("ALLOW_DEMO_PAYMENT_CONFIRM", true),
    allowDevApiKeyPrefix: parseBoolean("ALLOW_DEV_API_KEY_PREFIX", false),
    google: {
      clientId: required("GOOGLE_CLIENT_ID", "google-client-id"),
      clientSecret: required("GOOGLE_CLIENT_SECRET", "google-client-secret"),
      redirectUri: required("GOOGLE_REDIRECT_URI", "http://127.0.0.1:8080/v1/auth/google/callback"),
    },
    auth: {
      sessionTtlMinutes: parseNumber("AUTH_SESSION_TTL_MINUTES", 60 * 24 * 7),
      enforceTwoFactorSensitiveActions: parseBoolean("TWO_FACTOR_ENFORCE_SENSITIVE_ACTIONS", false),
      twoFactorVerificationWindowMinutes: parseNumber("TWO_FACTOR_VERIFICATION_WINDOW_MINUTES", 10),
    },
    webhookSecrets: {
      bkash: required("BKASH_WEBHOOK_SECRET", "bkash-secret"),
      sslcommerz: required("SSLCOMMERZ_WEBHOOK_SECRET", "sslcommerz-secret"),
    },
    bkash: {
      verifyEndpoint: optional("BKASH_VERIFY_ENDPOINT"),
      bearerToken: optional("BKASH_BEARER_TOKEN"),
    },
    sslcommerz: {
      validatorEndpoint: optional("SSLCOMMERZ_VALIDATOR_ENDPOINT"),
      storeId: optional("SSLCOMMERZ_STORE_ID"),
      storePassword: optional("SSLCOMMERZ_STORE_PASSWORD"),
    },
    supabase: {
      url: required("SUPABASE_URL", "http://127.0.0.1:54321"),
      serviceRoleKey: required("SUPABASE_SERVICE_ROLE_KEY", "dev-service-role"),
      flags: {
        authEnabled: parseBoolean("SUPABASE_AUTH_ENABLED", false),
        userRepoEnabled: parseBoolean("SUPABASE_USER_REPO_ENABLED", false),
        apiKeysEnabled: parseBoolean("SUPABASE_API_KEYS_ENABLED", false),
        billingStoreEnabled: parseBoolean("SUPABASE_BILLING_STORE_ENABLED", false),
      },
    },
    paymentReconciliation: {
      enabled: parseBoolean("PAYMENT_RECONCILIATION_ENABLED", false),
      intervalMs: parsePositiveInteger("PAYMENT_RECONCILIATION_INTERVAL_MS", 60 * 60 * 1000),
      lookbackHours: parsePositiveInteger("PAYMENT_RECONCILIATION_LOOKBACK_HOURS", 24),
    },
    providers: {
      circuitBreaker: {
        failureThreshold: parsePositiveInteger("PROVIDER_CB_THRESHOLD", 5),
        resetTimeoutMs: parsePositiveInteger("PROVIDER_CB_RESET_MS", 30000),
      },
      openrouter: {
        apiKey: optional("OPENROUTER_API_KEY"),
        baseUrl: required("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
        model: optional("OPENROUTER_MODEL") ?? optional("OPENROUTER_FREE_MODEL") ?? "openrouter/auto",
        freeModel: optional("OPENROUTER_FREE_MODEL"),
        timeoutMs: parsePositiveInteger("OPENROUTER_TIMEOUT_MS", providerTimeoutMs),
        maxRetries: parseNonNegativeInteger("OPENROUTER_MAX_RETRIES", providerMaxRetries),
      },
      ollama: {
        baseUrl: required("OLLAMA_BASE_URL", "http://127.0.0.1:11434"),
        model: required("OLLAMA_MODEL", "llama3.1:8b"),
        freeModel: optional("OLLAMA_FREE_MODEL"),
        timeoutMs: parsePositiveInteger("OLLAMA_TIMEOUT_MS", providerTimeoutMs),
        maxRetries: parseNonNegativeInteger("OLLAMA_MAX_RETRIES", providerMaxRetries),
      },
      groq: {
        apiKey: optional("GROQ_API_KEY"),
        baseUrl: required("GROQ_BASE_URL", "https://api.groq.com/openai/v1"),
        model: required("GROQ_MODEL", "llama-3.1-8b-instant"),
        freeModel: optional("GROQ_FREE_MODEL"),
        timeoutMs: parsePositiveInteger("GROQ_TIMEOUT_MS", providerTimeoutMs),
        maxRetries: parseNonNegativeInteger("GROQ_MAX_RETRIES", providerMaxRetries),
      },
      openai: {
        apiKey: optional("OPENAI_API_KEY"),
        baseUrl: required("OPENAI_BASE_URL", "https://api.openai.com/v1"),
        chatModel: required("OPENAI_CHAT_MODEL", "gpt-4o-mini"),
        imageModel: required("OPENAI_IMAGE_MODEL", "gpt-image-1"),
        freeModel: optional("OPENAI_FREE_MODEL"),
        timeoutMs: parsePositiveInteger("OPENAI_TIMEOUT_MS", providerTimeoutMs),
        maxRetries: parseNonNegativeInteger("OPENAI_MAX_RETRIES", providerMaxRetries),
      },
      gemini: {
        apiKey: optional("GEMINI_API_KEY"),
        baseUrl: required("GEMINI_BASE_URL", "https://generativelanguage.googleapis.com/v1beta/openai/"),
        model: required("GEMINI_MODEL", "gemini-3-flash-preview"),
        freeModel: optional("GEMINI_FREE_MODEL"),
        timeoutMs: parsePositiveInteger("GEMINI_TIMEOUT_MS", providerTimeoutMs),
        maxRetries: parseNonNegativeInteger("GEMINI_MAX_RETRIES", providerMaxRetries),
      },
      anthropic: {
        apiKey: optional("ANTHROPIC_API_KEY"),
        baseUrl: required("ANTHROPIC_BASE_URL", "https://api.anthropic.com/v1"),
        model: required("ANTHROPIC_MODEL", "claude-sonnet-4-5"),
        freeModel: optional("ANTHROPIC_FREE_MODEL"),
        timeoutMs: parsePositiveInteger("ANTHROPIC_TIMEOUT_MS", providerTimeoutMs),
        maxRetries: parseNonNegativeInteger("ANTHROPIC_MAX_RETRIES", providerMaxRetries),
      },
    },
    langfuse: {
      enabled: parseBoolean("LANGFUSE_ENABLED", false),
      baseUrl: required("LANGFUSE_BASE_URL", "https://cloud.langfuse.com"),
      publicKey: optional("LANGFUSE_PUBLIC_KEY"),
      secretKey: optional("LANGFUSE_SECRET_KEY"),
    },
  };

  if (env.nodeEnv === "production") {
    if (env.webhookSecrets.bkash === "bkash-secret" || env.webhookSecrets.sslcommerz === "sslcommerz-secret") {
      throw new Error("Production mode requires non-default webhook secrets");
    }
  }

  return env;
}
