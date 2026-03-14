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

    expect(service.pickDefault("chat").id).toBe("fast-chat");
  });

  it("picks the free guest chat model as the guest default", () => {
    const service = new ModelService();
    const guestModel = service.pickGuestDefault("chat");

    expect(guestModel.id).toBe("guest-free");
    expect(service.isGuestAccessible(guestModel.id)).toBe(true);
  });
});
