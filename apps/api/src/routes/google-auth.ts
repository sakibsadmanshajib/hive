import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

type GoogleCallbackQuery = {
  state?: string;
  code?: string;
};

export function registerGoogleAuthRoutes(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/auth/google/start", async () => {
    const result = await services.auth.startGoogleAuth();
    return {
      provider: "google",
      state: result.state,
      authorization_url: result.authorizationUrl,
    };
  });

  app.get<{ Querystring: GoogleCallbackQuery }>("/v1/auth/google/callback", async (request, reply) => {
    const result = await services.auth.completeGoogleAuth({
      state: request.query?.state,
      code: request.query?.code,
    });
    if (result.error) {
      return reply.code(400).send({ error: result.error });
    }

    return {
      session_token: result.sessionToken,
      user: {
        user_id: result.userId,
        email: result.email,
        name: result.name,
      },
    };
  });

  app.post("/v1/auth/logout", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {});
    if (!principal) {
      return;
    }
    const authHeader = request.headers.authorization;
    if (!authHeader || typeof authHeader !== "string") {
      return reply.code(400).send({ error: "authorization bearer token required" });
    }
    const parts = authHeader.split(" ");
    const token = parts.length === 2 ? parts[1] : "";
    if (!token) {
      return reply.code(400).send({ error: "authorization bearer token required" });
    }
    await services.auth.revokeSession(token);
    return { logged_out: true, user_id: principal.userId };
  });
}
