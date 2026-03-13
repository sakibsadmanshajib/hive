import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";

function isAuthorized(requestHeaders: Record<string, unknown> | undefined, expectedToken: string | undefined) {
  const providedToken = requestHeaders?.["x-admin-token"];
  return expectedToken && typeof providedToken === "string" && providedToken === expectedToken;
}

export function registerSupportRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/support/users/:userId", async (request, reply) => {
    if (!isAuthorized(request.headers, services.env.adminStatusToken)) {
      reply.code(401);
      return { error: "unauthorized" };
    }

    const params = (request.params ?? {}) as { userId?: string };
    if (!params.userId) {
      reply.code(400);
      return { error: "missing user id" };
    }

    const user = await services.users.me(params.userId);
    if (!user) {
      reply.code(404);
      return { error: "user not found" };
    }

    const usage = await services.usage.listWithSummary(params.userId);
    const credits = await services.credits.getBalance(params.userId);

    return {
      object: "support.user",
      data: {
        user: {
          userId: user.userId,
          email: user.email,
          name: user.name,
          createdAt: user.createdAt,
        },
        credits,
        usage,
        apiKeys: user.apiKeys,
        apiKeyEvents: user.apiKeyEvents,
      },
    };
  });
}
