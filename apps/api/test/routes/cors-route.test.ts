import { afterAll, describe, expect, it } from "vitest";
import { createApp } from "../../src/server";

describe("api cors", () => {
  const app = createApp();

  afterAll(async () => {
    await app.close();
  });

  it("responds to browser preflight from the local web app origin", async () => {
    const response = await app.inject({
      method: "OPTIONS",
      url: "/v1/models",
      headers: {
        origin: "http://127.0.0.1:3000",
        "access-control-request-method": "GET",
      },
    });

    expect(response.statusCode).toBe(204);
    expect(response.headers["access-control-allow-origin"]).toBe("http://127.0.0.1:3000");
  });

  it("rejects disallowed origins without surfacing a server error", async () => {
    const response = await app.inject({
      method: "OPTIONS",
      url: "/v1/models",
      headers: {
        origin: "http://malicious-site.com",
        "access-control-request-method": "GET",
      },
    });

    expect(response.statusCode).toBe(404);
    expect(response.headers["access-control-allow-origin"]).toBeUndefined();
  });
});
