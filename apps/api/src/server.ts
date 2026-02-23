import Fastify from "fastify";
import { createRuntimeServices } from "./runtime/services";
import { registerRoutes } from "./routes";

export function createApp() {
  const app = Fastify({ logger: true });
  const services = createRuntimeServices();
  registerRoutes(app, services);
  return app;
}
