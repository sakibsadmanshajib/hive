import { describe, expect, it } from "vitest";
import { AuthorizationService } from "../../src/runtime/authorization";

describe("AuthorizationService", () => {
  it("maps legacy api key scopes to equivalent permissions", async () => {
    const authorization = new AuthorizationService({
      listPermissionsForUser: async () => [],
    } as never);

    const allowed = await authorization.hasPermission({ userId: "user_1", scopes: ["chat"] }, "chat:write");

    expect(allowed).toBe(true);
  });

  it("allows permissions provided by role assignments", async () => {
    const authorization = new AuthorizationService({
      listPermissionsForUser: async () => ["billing:write"],
    } as never);

    const allowed = await authorization.hasPermission({ userId: "user_1", scopes: [] }, "billing:write");

    expect(allowed).toBe(true);
  });
});
