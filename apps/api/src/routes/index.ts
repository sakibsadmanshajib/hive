import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { registerAnalyticsRoute } from "./analytics";
import { registerChatCompletionsRoute } from "./chat-completions";
import { registerCreditsBalanceRoute } from "./credits-balance";
import { registerGuestAttributionRoutes } from "./guest-attribution";
import { registerGuestChatRoute } from "./guest-chat";
import { registerHealthRoute } from "./health";
import { registerImagesGenerationsRoute } from "./images-generations";
import { registerModelsRoute } from "./models";
import { registerPaymentIntentsRoute } from "./payment-intents";
import { registerPaymentDemoConfirmRoute } from "./payment-demo-confirm";
import { registerPaymentWebhookRoute } from "./payment-webhook";
import { registerProvidersMetricsRoute } from "./providers-metrics";
import { registerProvidersStatusRoute } from "./providers-status";
import { registerResponsesRoute } from "./responses";
import { registerSupportRoute } from "./support";
import { registerUserRoutes } from "./users";
import { registerUsageRoute } from "./usage";

export function registerRoutes(app: FastifyInstance, services: RuntimeServices): void {
  registerHealthRoute(app);
  registerAnalyticsRoute(app, services);
  registerModelsRoute(app, services);
  registerGuestAttributionRoutes(app, services);
  registerGuestChatRoute(app, services);
  registerChatCompletionsRoute(app, services);
  registerResponsesRoute(app, services);
  registerImagesGenerationsRoute(app, services);
  registerCreditsBalanceRoute(app, services);
  registerUsageRoute(app, services);
  registerUserRoutes(app, services);
  registerProvidersStatusRoute(app, services);
  registerProvidersMetricsRoute(app, services);
  registerSupportRoute(app, services);
  registerPaymentIntentsRoute(app, services);
  registerPaymentDemoConfirmRoute(app, services);
  registerPaymentWebhookRoute(app, services);
}
