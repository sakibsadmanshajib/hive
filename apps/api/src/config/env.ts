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
  if (raw === undefined) {
    return fallback;
  }
  const value = Number(raw);
  if (Number.isNaN(value)) {
    throw new Error(`Invalid numeric environment variable ${name}: ${raw}`);
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
  providers: {
    ollama: {
      baseUrl: string;
      model: string;
    };
    groq: {
      apiKey?: string;
      baseUrl: string;
      model: string;
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
  const env: AppEnv = {
    nodeEnv: process.env.NODE_ENV ?? "development",
    port: parseNumber("PORT", 8080),
    postgresUrl: required("POSTGRES_URL", "postgres://postgres:postgres@127.0.0.1:5432/bd_ai_gateway"),
    redisUrl: required("REDIS_URL", "redis://127.0.0.1:6379"),
    rateLimitPerMinute: parseNumber("RATE_LIMIT_PER_MINUTE", 60),
    adminStatusToken: process.env.ADMIN_STATUS_TOKEN,
    allowDemoPaymentConfirm: parseBoolean("ALLOW_DEMO_PAYMENT_CONFIRM", true),
    allowDevApiKeyPrefix: parseBoolean("ALLOW_DEV_API_KEY_PREFIX", false),
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
    providers: {
      ollama: {
        baseUrl: required("OLLAMA_BASE_URL", "http://127.0.0.1:11434"),
        model: required("OLLAMA_MODEL", "llama3.1:8b"),
      },
      groq: {
        apiKey: process.env.GROQ_API_KEY,
        baseUrl: required("GROQ_BASE_URL", "https://api.groq.com/openai/v1"),
        model: required("GROQ_MODEL", "llama-3.1-8b-instant"),
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
