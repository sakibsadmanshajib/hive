export type GatewayModel = {
  id: string;
  object: "model";
  capability: "chat" | "image";
  creditsPerRequest: number;
  provider?: "mock" | "ollama" | "groq";
};

export type UsageEvent = {
  id: string;
  userId: string;
  endpoint: string;
  model: string;
  credits: number;
  createdAt: string;
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

export type PersistentApiKey = {
  keyPrefix: string;
  userId: string;
  scopes: string[];
  revoked: boolean;
  createdAt: string;
};

export type PaymentIntent = {
  intentId: string;
  userId: string;
  provider: "bkash" | "sslcommerz";
  bdtAmount: number;
  status: "initiated" | "credited" | "failed";
};
