export type ModelCostType = "free" | "fixed" | "variable";
export type UsageChannel = "api" | "web";

export type GatewayModelPricing = {
  creditsPerRequest?: number;
  inputTokensPer1m?: number;
  outputTokensPer1m?: number;
  cacheReadTokensPer1m?: number;
  cacheWriteTokensPer1m?: number;
};

export type GatewayModel = {
  id: string;
  object: "model";
  created: number;
  capability: "chat" | "image" | "embedding";
  costType: ModelCostType;
  pricing: GatewayModelPricing;
};

export type UsageEvent = {
  id: string;
  userId: string;
  endpoint: string;
  model: string;
  credits: number;
  channel: UsageChannel;
  apiKeyId?: string;
  createdAt: string;
};

export type SessionUserIdentity = {
  userId: string;
  email: string;
  name?: string;
};

export type ChatMessageRole = "system" | "user" | "assistant";

export type PersistedChatMessage = {
  id: string;
  sessionId: string;
  role: ChatMessageRole;
  content: string;
  createdAt: string;
  sequence: number;
};

export type PersistedChatSessionSummary = {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  lastMessageAt: string | null;
};

export type PersistedChatSession = PersistedChatSessionSummary & {
  messages: PersistedChatMessage[];
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
  byChannel: UsageSummaryBucket[];
  byApiKey: UsageSummaryBucket[];
};

export type TrafficAnalyticsSnapshot = {
  windowDays: number;
  channels: {
    api: { requests: number; credits: number };
    web: { requests: number; credits: number };
  };
  byApiKey: UsageSummaryBucket[];
  webBreakdown: {
    guestRequests: number;
    authenticatedRequests: number;
    guestSessions: number;
    linkedGuests: number;
    conversionRate: number;
  };
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
