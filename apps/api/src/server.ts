import Fastify from "fastify";
import cors from "@fastify/cors";
import { createRuntimeServices } from "./runtime/services";
import { registerRoutes } from "./routes";

const DEFAULT_ALLOWED_ORIGINS = [
  "http://127.0.0.1:3000",
  "http://localhost:3000",
  "http://127.0.0.1:3001",
  "http://localhost:3001",
];

function readAllowedOrigins(): Set<string> {
  const configured = process.env.CORS_ALLOWED_ORIGINS
    ?.split(",")
    .map((origin) => origin.trim())
    .filter(Boolean);

  return new Set(configured && configured.length > 0 ? configured : DEFAULT_ALLOWED_ORIGINS);
}

export function createApp() {
  const app = Fastify({ logger: true });
  const allowedOrigins = readAllowedOrigins();

  void app.register(cors, {
    origin(origin, callback) {
      if (!origin || allowedOrigins.has(origin)) {
        callback(null, true);
        return;
      }

      callback(new Error(`origin not allowed: ${origin}`), false);
    },
    methods: ["GET", "POST", "PATCH", "OPTIONS"],
    allowedHeaders: ["authorization", "content-type", "x-api-key", "x-admin-token"],
  });

  const services = createRuntimeServices();
  registerRoutes(app, services);
  return app;
}
