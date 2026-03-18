import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { serializeModel } from "../domain/model-service";
import { ModelsParamsSchema, type ModelsParams } from "../schemas/models";
import { sendApiError } from "./api-error";

export function registerModelsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/models", async () => {
    return {
      object: "list" as const,
      data: services.models.list().map(serializeModel),
    };
  });

  app.get<{ Params: ModelsParams }>("/v1/models/:model", {
    schema: { params: ModelsParamsSchema },
  }, async (request, reply) => {
    const model = services.models.findById(request.params.model);
    if (!model) {
      return sendApiError(reply, 404,
        `The model '${request.params.model}' does not exist`,
        { type: "invalid_request_error", code: "model_not_found" });
    }
    return serializeModel(model);
  });
}
