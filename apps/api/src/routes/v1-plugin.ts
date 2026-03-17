import type { FastifyInstance, FastifyError } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { STATUS_TO_TYPE } from "./api-error";

export async function v1Plugin(
  app: FastifyInstance,
  _opts: { services: RuntimeServices },
): Promise<void> {
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
}

// Break encapsulation so error/not-found handlers apply to the registering scope.
// This is equivalent to wrapping with fastify-plugin.
// In production, v1Plugin is registered inside a scoped app.register() call
// that provides the encapsulation boundary for /v1/* routes.
(v1Plugin as any)[Symbol.for("skip-override")] = true;
