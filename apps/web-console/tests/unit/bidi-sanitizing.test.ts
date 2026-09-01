import { describe, expect, it, vi } from "vitest";

import { stripBidiControls } from "@/lib/text/bidi";

// U+202E RIGHT-TO-LEFT OVERRIDE: the character found stored in a live API key
// nickname, rendering unescaped on /console/logs and visually reordering the
// labels around it (issue #1653).
const RLO = "\u202E";

vi.mock("next/headers", () => ({
  cookies: async () => ({ get: () => undefined }),
}));

vi.mock("@/lib/supabase/server", () => ({
  createClient: () => ({
    auth: {
      getUser: async () => ({ data: { user: { id: "user-1" } }, error: null }),
      getSession: async () => ({
        data: { session: { access_token: "test-token" } },
      }),
    },
  }),
}));

describe("stripBidiControls", () => {
  it("removes the override that reorders the characters after it", () => {
    expect(stripBidiControls(`prod${RLO}gnp.txt`)).toBe("prodgnp.txt");
  });

  it("removes the marks, the embeddings and the isolates as well", () => {
    const every =
      "a\u061Cb\u200Ec\u200Fd\u202Ae\u202Bf\u202Cg\u202Dh\u2066i\u2067j\u2068k\u2069l";
    expect(stripBidiControls(every)).toBe("abcdefghijkl");
  });

  it("leaves ordinary text and genuine right-to-left letters alone", () => {
    expect(stripBidiControls("staging key")).toBe("staging key");
    // Arabic letters carry their own strong direction, so there is nothing to
    // strip and nothing to break.
    expect(stripBidiControls("مفتاح")).toBe("مفتاح");
  });
});

describe("control-plane decode boundary", () => {
  it("strips a stored bidi override out of an API key nickname", async () => {
    process.env.CONTROL_PLANE_BASE_URL = "http://control-plane.test";
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              items: [
                {
                  id: "key-1",
                  nickname: `prod${RLO}gnp.txt`,
                  status: "active",
                  redacted_suffix: "9f2a",
                },
              ],
            }),
            { status: 200 },
          ),
      ),
    );

    const { getApiKeys } = await import("@/lib/control-plane/client");
    const keys = await getApiKeys();

    // The row is already in the database, so input validation upstream cannot
    // reach it. The decode boundary is what every console surface reads
    // through, so cleaning it here covers the logs filter, the usage table and
    // the CSV export in one place.
    expect(keys).toHaveLength(1);
    expect(keys[0].nickname).toBe("prodgnp.txt");
    expect(keys[0].nickname.includes(RLO)).toBe(false);

    vi.unstubAllGlobals();
  });
});
