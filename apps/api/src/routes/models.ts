import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";

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
