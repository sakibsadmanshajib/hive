import { config as loadDotEnv } from "dotenv";

loadDotEnv();

function required(name: string, fallback?: string): string {
  const value = process.env[name] ?? fallback;
  if (!value) {
    throw new Error(`Missing required environment variable: ${name}`);
  }
  return value;
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
  providers: {
    circuitBreaker: {
      failureThreshold: number;
      resetTimeoutMs: number;
    };
    ollama: {
      baseUrl: string;
      model: string;
      timeoutMs: number;
      maxRetries: number;
    };
          groq: {
            apiKey?: string;
            baseUrl: string;
            model: string;
            timeoutMs: number;
            maxRetries: number;
          };
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
      verifyEndpoint: process.env.BKASH_VERIFY_ENDPOINT,
      bearerToken: process.env.BKASH_BEARER_TOKEN,
    },
    sslcommerz: {
      validatorEndpoint: process.env.SSLCOMMERZ_VALIDATOR_ENDPOINT,
      storeId: process.env.SSLCOMMERZ_STORE_ID,
      storePassword: process.env.SSLCOMMERZ_STORE_PASSWORD,
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
    providers: {
      circuitBreaker: {
        failureThreshold: parsePositiveInteger("PROVIDER_CB_THRESHOLD", 5),
        resetTimeoutMs: parsePositiveInteger("PROVIDER_CB_RESET_MS", 30000),
      },
      ollama: {
        baseUrl: required("OLLAMA_BASE_URL", "http://127.0.0.1:11434"),
        model: required("OLLAMA_MODEL", "llama3.1:8b"),
        timeoutMs: parsePositiveInteger("OLLAMA_TIMEOUT_MS", providerTimeoutMs),
        maxRetries: parseNonNegativeInteger("OLLAMA_MAX_RETRIES", providerMaxRetries),
      },
      groq: {
        apiKey: process.env.GROQ_API_KEY,
        baseUrl: required("GROQ_BASE_URL", "https://api.groq.com/openai/v1"),
        model: required("GROQ_MODEL", "llama-3.1-8b-instant"),
        timeoutMs: parsePositiveInteger("GROQ_TIMEOUT_MS", providerTimeoutMs),
        maxRetries: parseNonNegativeInteger("GROQ_MAX_RETRIES", providerMaxRetries),
      },
    },
    langfuse: {
      enabled: parseBoolean("LANGFUSE_ENABLED", false),
      baseUrl: required("LANGFUSE_BASE_URL", "https://cloud.langfuse.com"),
      publicKey: process.env.LANGFUSE_PUBLIC_KEY,
      secretKey: process.env.LANGFUSE_SECRET_KEY,
    },
  };

  if (env.nodeEnv === "production") {
    if (env.webhookSecrets.bkash === "bkash-secret" || env.webhookSecrets.sslcommerz === "sslcommerz-secret") {
      throw new Error("Production mode requires non-default webhook secrets");
    }
  }

  return env;
}
