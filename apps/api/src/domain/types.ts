export type GatewayModel = {
  id: string;
  object: "model";
  capability: "chat" | "image";
  creditsPerRequest: number;
  provider?: "mock" | "ollama" | "groq" | "openai";
};

export type UsageEvent = {
  id: string;
  userId: string;
  endpoint: string;
  model: string;
  credits: number;
  createdAt: string;
};

export type UsageSummaryBucket = {
  key: string;
  requests: number;
  credits: number;
};

export type UsageDailyTrendPoint = {
  date: string;
  requests: number;
  credits: number;
};

export type UsageSummary = {
  windowDays: number;
  totalRequests: number;
  totalCredits: number;
  daily: UsageDailyTrendPoint[];
  byModel: UsageSummaryBucket[];
  byEndpoint: UsageSummaryBucket[];
};

export type CreditBalance = {
  userId: string;
  availableCredits: number;
  purchasedCredits: number;
  promoCredits: number;
};

export type PersistentPaymentIntent = {
  intentId: string;
  userId: string;
  provider: "bkash" | "sslcommerz";
  bdtAmount: number;
  status: "initiated" | "credited" | "failed";
  mintedCredits: number;
};

export type PaymentReconciliationIntent = PersistentPaymentIntent & {
  paymentLedgerCredits: number;
  createdAt: string;
};

export type PaymentReconciliationEvent = {
  eventKey: string;
  intentId: string;
  provider: "bkash" | "sslcommerz";
  providerTxnId: string;
  verified: boolean;
  createdAt: string;
};

export type PaymentReconciliationSnapshot = {
  intents: PaymentReconciliationIntent[];
  events: PaymentReconciliationEvent[];
};

export type PaymentReconciliationFinding =
  | {
    kind: "verified_event_without_credited_intent";
    intentId: string;
    provider: "bkash" | "sslcommerz";
    providerTxnId: string;
  }
  | {
    kind: "credited_intent_without_verified_event";
    intentId: string;
    provider: "bkash" | "sslcommerz";
  }
  | {
    kind: "credited_amount_mismatch";
    intentId: string;
    provider: "bkash" | "sslcommerz";
    expectedMintedCredits: number;
    actualMintedCredits: number;
    actualLedgerCredits: number;
  }
  | {
    kind: "missing_payment_ledger_entry";
    intentId: string;
    provider: "bkash" | "sslcommerz";
  };

export type PaymentReconciliationSummary = {
  totalFindings: number;
  verifiedEventWithoutCreditedIntent: number;
  creditedIntentWithoutVerifiedEvent: number;
  creditedAmountMismatch: number;
  missingPaymentLedgerEntry: number;
};

export type PaymentReconciliationResult = {
  summary: PaymentReconciliationSummary;
  findings: PaymentReconciliationFinding[];
};

export type PersistentApiKey = {
  id: string;
  keyPrefix: string;
  nickname: string;
  userId: string;
  scopes: string[];
  status: "active" | "revoked" | "expired";
  revoked: boolean;
  createdAt: string;
  expiresAt?: string;
  revokedAt?: string;
};

export type PersistentApiKeyEvent = {
  id: string;
  apiKeyId: string;
  userId: string;
  eventType: "created" | "revoked" | "expired_observed";
  eventAt: string;
  metadata: Record<string, unknown>;
};

export type PaymentIntent = {
  intentId: string;
  userId: string;
  provider: "bkash" | "sslcommerz";
  bdtAmount: number;
  status: "initiated" | "credited" | "failed";
};
