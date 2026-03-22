import { describe, expect, it } from "vitest";
import { ModelService } from "../../src/domain/model-service";

describe("model service", () => {
  it("marks only free chat models as guest-accessible", () => {
    const service = new ModelService();

    expect(service.isGuestAccessible("guest-free")).toBe(true);
    expect(service.isGuestAccessible("fast-chat")).toBe(false);
    expect(service.isGuestAccessible("smart-reasoning")).toBe(false);
  });

  it("uses a paid chat model as the authenticated default", () => {
    const service = new ModelService();

    expect(service.pickDefault("chat").id).toBe("smart-reasoning");
  });

  it("picks the free guest chat model as the guest default", () => {
    const service = new ModelService();
    const guestModel = service.pickGuestDefault("chat");

    expect(guestModel.id).toBe("guest-free");
    expect(service.isGuestAccessible(guestModel.id)).toBe(true);
  });

  it("fails closed when free models are disabled", () => {
    const service = new ModelService({ enabledFreeModelIds: [] });

    expect(service.findById("guest-free")).toBeUndefined();
    expect(service.isGuestAccessible("guest-free")).toBe(false);
    expect(() => service.pickGuestDefault("chat")).toThrowError(/No guest model/);
  });

  it("resolves the canonical public embeddings model id", () => {
    const service = new ModelService();

    expect(service.findById("text-embedding-3-small")?.id).toBe("text-embedding-3-small");
  });

  it("resolves the legacy embeddings alias to the canonical public id", () => {
    const service = new ModelService();

    expect(service.findById("text-embedding-ada-002")?.id).toBe("text-embedding-3-small");
  });

  it("lists only the canonical public embeddings id", () => {
    const service = new ModelService();
    const modelIds = service.list().map((model) => model.id);

    expect(modelIds).toContain("text-embedding-3-small");
    expect(modelIds).not.toContain("openai/text-embedding-3-small");
  });
});
