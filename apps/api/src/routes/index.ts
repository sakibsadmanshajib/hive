import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { registerChatCompletionsRoute } from "./chat-completions";
import { registerCreditsBalanceRoute } from "./credits-balance";
import { registerGoogleAuthRoutes } from "./google-auth";
import { registerHealthRoute } from "./health";
import { registerImagesGenerationsRoute } from "./images-generations";
import { registerModelsRoute } from "./models";
import { registerPaymentIntentsRoute } from "./payment-intents";
import { registerPaymentDemoConfirmRoute } from "./payment-demo-confirm";
import { registerPaymentWebhookRoute } from "./payment-webhook";
import { registerProvidersStatusRoute } from "./providers-status";
import { registerResponsesRoute } from "./responses";
import { registerTwoFactorRoutes } from "./two-factor";
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
  registerProvidersStatusRoute(app, services);
  registerGoogleAuthRoutes(app, services);
  registerTwoFactorRoutes(app, services);
  registerUserRoutes(app, services);
  registerPaymentIntentsRoute(app, services);
  registerPaymentDemoConfirmRoute(app, services);
  registerPaymentWebhookRoute(app, services);
}
