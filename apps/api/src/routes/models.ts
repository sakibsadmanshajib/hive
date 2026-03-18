import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
// ModelsParamsSchema available in ../schemas/models.ts for /v1/models/:model (Phase 4)

export function registerModelsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/models", async () => {
    return {
      object: "list",
      data: services.models.list().map((model) => ({
        id: model.id,
        object: model.object,
        capability: model.capability,
        costType: model.costType,
      })),
    };
  });
}
