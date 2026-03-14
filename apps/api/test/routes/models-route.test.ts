import { describe, expect, it } from "vitest";
import { registerModelsRoute } from "../../src/routes/models";

class FakeApp {
  readonly handlers = new Map<string, (request?: any, reply?: any) => Promise<unknown> | unknown>();

  get(path: string, handler: (request?: any, reply?: any) => Promise<unknown> | unknown) {
    this.handlers.set(`GET ${path}`, handler);
  }
}

describe("models route", () => {
  it("returns cost metadata needed by the web app", async () => {
    const app = new FakeApp();
    registerModelsRoute(app as never, {
      models: {
        list: () => [
          { id: "guest-free", object: "model", capability: "chat", costType: "free" },
          { id: "smart-reasoning", object: "model", capability: "chat", costType: "variable" },
        ],
      },
    } as never);

    const handler = app.handlers.get("GET /v1/models");

    await expect(handler?.()).resolves.toEqual({
      object: "list",
      data: [
        { id: "guest-free", object: "model", capability: "chat", costType: "free" },
        { id: "smart-reasoning", object: "model", capability: "chat", costType: "variable" },
      ],
    });
  });
});
