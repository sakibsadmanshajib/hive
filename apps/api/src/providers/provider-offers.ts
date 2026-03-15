import type { AppEnv } from "../config/env";
import type { ProviderCostClass, ProviderName } from "./types";

export type ProviderOffer = {
  id: string;
  provider: ProviderName;
  capability: "chat";
  upstreamModel: string;
  costClass: ProviderCostClass;
};

export type ProviderOfferCatalog = {
  offers: Record<string, ProviderOffer>;
  modelOffers: Record<string, string[]>;
};

function addChatOffer(
  offers: Record<string, ProviderOffer>,
  offerIds: string[],
  input: {
    id: string;
    provider: ProviderName;
    upstreamModel?: string;
    costClass: ProviderCostClass;
  },
) {
  if (!input.upstreamModel) {
    return;
  }

  offers[input.id] = {
    id: input.id,
    provider: input.provider,
    capability: "chat",
    upstreamModel: input.upstreamModel,
    costClass: input.costClass,
  };
  offerIds.push(input.id);
}

export function buildProviderOfferCatalog(env: AppEnv): ProviderOfferCatalog {
  const providers = env.providers as Partial<AppEnv["providers"]>;
  const offers: Record<string, ProviderOffer> = {};
  const guestFreeOffers: string[] = [];

  addChatOffer(offers, guestFreeOffers, {
    id: "ollama-free",
    provider: "ollama",
    upstreamModel: providers.ollama?.freeModel,
    costClass: "zero",
  });
  addChatOffer(offers, guestFreeOffers, {
    id: "openrouter-free",
    provider: "openrouter",
    upstreamModel: providers.openrouter?.freeModel,
    costClass: "zero",
  });
  addChatOffer(offers, guestFreeOffers, {
    id: "openai-free",
    provider: "openai",
    upstreamModel: providers.openai?.freeModel,
    costClass: "zero",
  });
  addChatOffer(offers, guestFreeOffers, {
    id: "groq-free",
    provider: "groq",
    upstreamModel: providers.groq?.freeModel,
    costClass: "zero",
  });
  addChatOffer(offers, guestFreeOffers, {
    id: "gemini-free",
    provider: "gemini",
    upstreamModel: providers.gemini?.freeModel,
    costClass: "zero",
  });
  addChatOffer(offers, guestFreeOffers, {
    id: "anthropic-free",
    provider: "anthropic",
    upstreamModel: providers.anthropic?.freeModel,
    costClass: "zero",
  });

  return {
    offers,
    modelOffers: {
      "guest-free": guestFreeOffers,
    },
  };
}
