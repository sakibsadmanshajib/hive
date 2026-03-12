import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { registerChatCompletionsRoute } from "./chat-completions";
import { registerCreditsBalanceRoute } from "./credits-balance";
import { registerHealthRoute } from "./health";
import { registerImagesGenerationsRoute } from "./images-generations";
import { registerModelsRoute } from "./models";
import { registerPaymentIntentsRoute } from "./payment-intents";
import { registerPaymentDemoConfirmRoute } from "./payment-demo-confirm";
import { registerPaymentWebhookRoute } from "./payment-webhook";
import { registerProvidersMetricsRoute } from "./providers-metrics";
import { registerProvidersStatusRoute } from "./providers-status";
import { registerResponsesRoute } from "./responses";
import { registerUserRoutes } from "./users";
import { registerUsageRoute } from "./usage";

export function registerRoutes(app: FastifyInstance, services: RuntimeServices): void {
  registerHealthRoute(app);
  registerModelsRoute(app, services);
  registerChatCompletionsRoute(app, services);
  registerResponsesRoute(app, services);
  registerImagesGenerationsRoute(app, services);
  registerCreditsBalanceRoute(app, services);
  registerUsageRoute(app, services);
  registerUserRoutes(app, services);
  registerProvidersStatusRoute(app, services);
  registerProvidersMetricsRoute(app, services);
  registerPaymentIntentsRoute(app, services);
  registerPaymentDemoConfirmRoute(app, services);
  registerPaymentWebhookRoute(app, services);
}
