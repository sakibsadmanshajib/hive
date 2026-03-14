import Fastify from "fastify";
import cors from "@fastify/cors";
import { createRuntimeServices } from "./runtime/services";
import { registerRoutes } from "./routes";
import { readAllowedOrigins } from "./runtime/cors-origins";

export function createApp() {
  const app = Fastify({ logger: true });
  const allowedOrigins = readAllowedOrigins();

  void app.register(cors, {
    origin(origin, callback) {
      if (!origin || allowedOrigins.has(origin)) {
        callback(null, true);
        return;
      }

      callback(null, false);
    },
    methods: ["GET", "POST", "PATCH", "OPTIONS"],
    allowedHeaders: ["authorization", "content-type", "x-api-key", "x-admin-token"],
  });

  const services = createRuntimeServices();
  registerRoutes(app, services);
  return app;
}
