import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { registerAnalyticsRoute } from "./analytics";
import { registerChatSessionsRoute } from "./chat-sessions";
import { registerCreditsBalanceRoute } from "./credits-balance";
import { registerGuestAttributionRoutes } from "./guest-attribution";
import { registerGuestChatRoute } from "./guest-chat";
import { registerGuestChatSessionsRoute } from "./guest-chat-sessions";
import { registerHealthRoute } from "./health";
import { registerPaymentIntentsRoute } from "./payment-intents";
import { registerPaymentDemoConfirmRoute } from "./payment-demo-confirm";
import { registerPaymentWebhookRoute } from "./payment-webhook";
import { registerProvidersMetricsRoute } from "./providers-metrics";
import { registerProvidersStatusRoute } from "./providers-status";
import { registerSupportRoute } from "./support";
import { registerUserRoutes } from "./users";
import { registerUsageRoute } from "./usage";
import { v1Plugin } from "./v1-plugin";

export function registerRoutes(app: FastifyInstance, services: RuntimeServices): void {
  // OpenAI-facing API routes (scoped error handler)
  void app.register(v1Plugin, { services });

  // Web pipeline routes (keep flat error format)
  registerHealthRoute(app);
  registerAnalyticsRoute(app, services);
  registerGuestAttributionRoutes(app, services);
  registerGuestChatRoute(app, services);
  registerGuestChatSessionsRoute(app, services);
  registerChatSessionsRoute(app, services);
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
