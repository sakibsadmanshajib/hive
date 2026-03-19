import { randomUUID } from "node:crypto";
import type { FastifyInstance, FastifyError } from "fastify";
import type { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import type { RuntimeServices } from "../runtime/services";
import { STATUS_TO_TYPE } from "./api-error";
import { registerChatCompletionsRoute } from "./chat-completions";
import { registerModelsRoute } from "./models";
import { registerEmbeddingsRoute } from "./embeddings";
import { registerImagesGenerationsRoute } from "./images-generations";
import { registerResponsesRoute } from "./responses";
import { registerV1StubRoutes } from "./v1-stubs";

export async function v1Plugin(
  app: FastifyInstance<any, any, any, any, TypeBoxTypeProvider>,
  opts: { services: RuntimeServices },
): Promise<void> {
  const { services } = opts;

  app.addHook('onRequest', async (_request, reply) => {
    reply.header('x-request-id', randomUUID());
  });

  app.setErrorHandler((error: FastifyError, _request, reply) => {
    const status = error.statusCode ?? 500;
    const message = error.message || "Internal server error";
    reply.code(status).send({
      error: {
        message,
        type: STATUS_TO_TYPE[status] ?? "server_error",
        param: null,
        code: null,
      },
    });
  });

  app.setNotFoundHandler((_request, reply) => {
    reply.code(404).send({
      error: {
        message: `Unknown API route: ${_request.method} ${_request.url}`,
        type: "not_found_error",
        param: null,
        code: null,
      },
    });
  });

  app.addHook('onSend', async (_request, reply, payload) => {
    const ct = reply.getHeader('content-type');
    if (typeof ct === 'string' && ct.includes('text/event-stream')) {
      return payload;
    }
    reply.header('content-type', 'application/json; charset=utf-8');
    return payload;
  });

  registerChatCompletionsRoute(app, services);
  registerModelsRoute(app, services);
  registerEmbeddingsRoute(app, services);
  registerImagesGenerationsRoute(app, services);
  registerResponsesRoute(app, services);
  registerV1StubRoutes(app);
}

// Break encapsulation so error/not-found handlers apply to the registering scope.
// This is equivalent to wrapping with fastify-plugin.
// In production, v1Plugin is registered inside a scoped app.register() call
// that provides the encapsulation boundary for /v1/* routes.
(v1Plugin as any)[Symbol.for("skip-override")] = true;
