import type { FastifyInstance, FastifyReply } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { serializeModel } from "../domain/model-service";
import { ModelsParamsSchema, type ModelsParams } from "../schemas/models";
import { requireV1ApiPrincipal } from "./auth";
import { sendApiError } from "./api-error";

function setModelsRouteHeaders(reply: FastifyReply): void {
  reply
    .header("x-model-routed", "")
    .header("x-provider-used", "")
    .header("x-provider-model", "")
    .header("x-actual-credits", "0");
}

export function registerModelsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/models", async (request, reply) => {
    setModelsRouteHeaders(reply);

    const principal = await requireV1ApiPrincipal(request, reply, services);
    if (!principal) {
      return;
    }

    return {
      object: "list" as const,
      data: services.models.list().map(serializeModel),
    };
  });

  app.get<{ Params: ModelsParams }>("/v1/models/:model", {
    schema: { params: ModelsParamsSchema },
  }, async (request, reply) => {
    setModelsRouteHeaders(reply);

    const principal = await requireV1ApiPrincipal(request, reply, services);
    if (!principal) {
      return;
    }

    const model = services.models.findById(request.params.model);
    if (!model) {
      return sendApiError(reply, 404,
        `The model '${request.params.model}' does not exist`,
        { type: "invalid_request_error", code: "model_not_found" });
    }
    return serializeModel(model);
  });
}
