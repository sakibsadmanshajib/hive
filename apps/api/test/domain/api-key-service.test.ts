import { describe, expect, it } from "vitest";

import { ApiKeyService } from "../../src/domain/api-key-service";

describe("ApiKeyService", () => {
  it("validates key when required scope is present", () => {
    const service = new ApiKeyService();
    const key = service.issueKey("user-1", ["read", "write"]);

    const result = service.validateKey(key, "read");

    expect(result).toBe("user-1");
  });

  it("rejects key when scope is missing", () => {
    const service = new ApiKeyService();
    const key = service.issueKey("user-2", ["read"]);

    const result = service.validateKey(key, "write");

    expect(result).toBeNull();
  });

  it("rejects revoked keys", () => {
    const service = new ApiKeyService();
    const key = service.issueKey("user-3", ["read"]);

    service.revokeKey(key);

    const result = service.validateKey(key, "read");
    expect(result).toBeNull();
  });
});
