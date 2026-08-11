import { describe, expect, it } from "vitest";

import {
  DEMO_SAFE_UI_SETTINGS,
  driftedKeys,
} from "./demo-chat-settings.mjs";

describe("driftedKeys", () => {
  it("reports the setting that broke the demo on 2026-08-11", () => {
    expect(driftedKeys({ ctrlEnterToSend: true })).toEqual([
      { key: "ctrlEnterToSend", expected: false, actual: true },
    ]);
  });

  it("passes an account that has the demo-safe value", () => {
    expect(driftedKeys({ ctrlEnterToSend: false })).toEqual([]);
  });

  it("treats an absent key as fine, because Open WebUI reads it through a default", () => {
    expect(driftedKeys({})).toEqual([]);
    expect(driftedKeys(null)).toEqual([]);
    expect(driftedKeys(undefined)).toEqual([]);
  });

  it("ignores every setting outside the demo-safe list", () => {
    expect(driftedKeys({ chatBubble: false, widescreenMode: true })).toEqual([]);
  });

  it("keeps the demo-safe list to settings whose wrong value is silent", () => {
    // A guard on the list itself: each entry costs an operator the freedom to
    // set that preference on a shared account, so growth here should be
    // deliberate rather than incidental.
    expect(Object.keys(DEMO_SAFE_UI_SETTINGS)).toEqual(["ctrlEnterToSend"]);
  });
});
