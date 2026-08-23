import { describe, expect, it } from "vitest";

import {
  CONSENT_LANDING_PATH,
  buildSignInRedirect,
  decideConsentLanding,
  isSafeRedirectTarget,
  lookupGoTrueAuthorization,
  parseGoTrueAuthorizationBody,
} from "./silent-consent";
import type { GoTrueAuthorization, GoTrueLookup, JsonFetch } from "./silent-consent";

const AUTH_ID = "auth-req-abc123";
const CHAT_CALLBACK = "https://chat-hive.scubed.co/oauth/callback?code=x";

function okLookup(authorization: GoTrueAuthorization): GoTrueLookup {
  return { status: "ok", authorization };
}

describe("decideConsentLanding", () => {
  it("returns the silent redirect when session is valid and consent already given", () => {
    const decision = decideConsentLanding({
      hasSession: true,
      authorizationId: AUTH_ID,
      lookup: okLookup({ kind: "auto-approved", redirectUrl: CHAT_CALLBACK }),
    });
    expect(decision).toEqual({
      action: "silent-redirect",
      url: CHAT_CALLBACK,
    });
  });

  it("renders the interactive panel when session is valid but consent is required", () => {
    const decision = decideConsentLanding({
      hasSession: true,
      authorizationId: AUTH_ID,
      lookup: okLookup({
        kind: "needs-consent",
        clientName: "Hive Chat",
        scope: "openid email profile",
      }),
    });
    expect(decision).toEqual({ action: "render-panel" });
  });

  it("falls through to the panel when there is no session", () => {
    const decision = decideConsentLanding({
      hasSession: false,
      authorizationId: AUTH_ID,
      lookup: null,
    });
    expect(decision).toEqual({ action: "render-panel" });
  });

  it("falls through to the panel when the request id is missing", () => {
    const decision = decideConsentLanding({
      hasSession: true,
      authorizationId: null,
      lookup: null,
    });
    expect(decision).toEqual({ action: "render-panel" });
  });

  it("routes to sign-in with a reason on a GoTrue 401 rejection", () => {
    const decision = decideConsentLanding({
      hasSession: true,
      authorizationId: AUTH_ID,
      lookup: { status: "rejected" },
    });
    expect(decision).toEqual({
      action: "sign-in",
      url: buildSignInRedirect(AUTH_ID, "silent-consent-session-expired"),
    });
  });

  it("falls back to the panel when GoTrue fails outright", () => {
    const decision = decideConsentLanding({
      hasSession: true,
      authorizationId: AUTH_ID,
      lookup: { status: "failed" },
    });
    expect(decision).toEqual({ action: "render-panel" });
  });

  it("refuses a redirect target that points back at this landing (loop bound)", () => {
    const decision = decideConsentLanding({
      hasSession: true,
      authorizationId: AUTH_ID,
      lookup: okLookup({
        kind: "auto-approved",
        redirectUrl: "https://console-hive.scubed.co/oauth/consent?authorization_id=again",
      }),
    });
    expect(decision.action).toBe("error");
  });

  it("refuses a non-http(s) redirect target", () => {
    const decision = decideConsentLanding({
      hasSession: true,
      authorizationId: AUTH_ID,
      lookup: okLookup({
        kind: "auto-approved",
        redirectUrl: "javascript:alert(1)",
      }),
    });
    expect(decision.action).toBe("error");
  });

  it("refuses a relative redirect target", () => {
    const decision = decideConsentLanding({
      hasSession: true,
      authorizationId: AUTH_ID,
      lookup: okLookup({ kind: "auto-approved", redirectUrl: "/somewhere" }),
    });
    expect(decision.action).toBe("error");
  });
});

describe("parseGoTrueAuthorizationBody", () => {
  it("parses an auto-approved body", () => {
    const parsed = parseGoTrueAuthorizationBody({
      redirect_url: CHAT_CALLBACK,
    });
    expect(parsed).toEqual({ kind: "auto-approved", redirectUrl: CHAT_CALLBACK });
  });

  it("parses a needs-consent body with client name and scope", () => {
    const parsed = parseGoTrueAuthorizationBody({
      client: { name: "Hive Chat" },
      scope: "openid email profile offline_access",
    });
    expect(parsed).toEqual({
      kind: "needs-consent",
      clientName: "Hive Chat",
      scope: "openid email profile offline_access",
    });
  });

  it("returns null for garbage", () => {
    expect(parseGoTrueAuthorizationBody("nope")).toBeNull();
    expect(parseGoTrueAuthorizationBody(42)).toBeNull();
    expect(parseGoTrueAuthorizationBody({})).toBeNull();
    expect(
      parseGoTrueAuthorizationBody({ redirect_url: "" }),
    ).toBeNull();
    expect(
      parseGoTrueAuthorizationBody({ client: { name: "" }, scope: "openid" }),
    ).toBeNull();
    expect(
      parseGoTrueAuthorizationBody({ client: "not-an-object", scope: "openid" }),
    ).toBeNull();
  });
});

describe("isSafeRedirectTarget", () => {
  it("accepts an absolute https target off the landing path", () => {
    expect(isSafeRedirectTarget(CHAT_CALLBACK)).toBe(true);
  });

  it("rejects the consent landing path itself regardless of origin", () => {
    expect(
      isSafeRedirectTarget("https://anything.example/oauth/consent"),
    ).toBe(false);
    expect(CONSENT_LANDING_PATH).toBe("/oauth/consent");
  });

  it("rejects relative and non-http(s) targets", () => {
    expect(isSafeRedirectTarget("/relative")).toBe(false);
    expect(isSafeRedirectTarget("data:text/html,hi")).toBe(false);
    expect(isSafeRedirectTarget("javascript:void(0)")).toBe(false);
    expect(isSafeRedirectTarget("not a url")).toBe(false);
  });
});

describe("buildSignInRedirect", () => {
  it("preserves authorization_id through the sign-in round-trip", () => {
    expect(buildSignInRedirect(AUTH_ID)).toBe(
      `/auth/sign-in?next=${encodeURIComponent(`/oauth/consent?authorization_id=${AUTH_ID}`)}`,
    );
  });

  it("appends the reason marker when one is given", () => {
    const url = buildSignInRedirect(AUTH_ID, "stale");
    expect(url.startsWith(`/auth/sign-in?next=`)).toBe(true);
    expect(url.endsWith("&reason=stale")).toBe(true);
  });
});

describe("lookupGoTrueAuthorization", () => {
  const config = { baseUrl: "https://sb.example/auth/v1", anonKey: "anon" };

  function jsonFetch(
    status: number,
    body: unknown,
    capture?: { url?: string; headers?: Record<string, string> },
  ): JsonFetch {
    return (url, init) => {
      if (capture) {
        capture.url = url;
        capture.headers = init?.headers ?? {};
      }
      return Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => {
          if (body instanceof Error) throw body;
          return Promise.resolve(body);
        },
      });
    };
  }

  it("sends the bearer token and returns an auto-approved authorization", async () => {
    const capture: { url?: string; headers?: Record<string, string> } = {};
    const result = await lookupGoTrueAuthorization(
      AUTH_ID,
      "tok",
      config,
      jsonFetch(200, { redirect_url: CHAT_CALLBACK }, capture),
    );
    expect(result).toEqual({
      status: "ok",
      authorization: { kind: "auto-approved", redirectUrl: CHAT_CALLBACK },
    });
    expect(capture.url).toBe(`${config.baseUrl}/oauth/authorizations/${AUTH_ID}`);
    expect(capture.headers).toMatchObject({ Authorization: "Bearer tok" });
  });

  it("maps 401 and 403 to rejected", async () => {
    expect(
      await lookupGoTrueAuthorization(AUTH_ID, "t", config, jsonFetch(401, {})),
    ).toEqual({ status: "rejected" });
    expect(
      await lookupGoTrueAuthorization(AUTH_ID, "t", config, jsonFetch(403, {})),
    ).toEqual({ status: "rejected" });
  });

  it("maps a 500, a malformed body, and a network throw to failed", async () => {
    expect(
      await lookupGoTrueAuthorization(AUTH_ID, "t", config, jsonFetch(500, {})),
    ).toEqual({ status: "failed" });
    expect(
      await lookupGoTrueAuthorization(AUTH_ID, "t", config, jsonFetch(200, "garbage")),
    ).toEqual({ status: "failed" });
    expect(
      await lookupGoTrueAuthorization(AUTH_ID, "t", config, jsonFetch(200, new Error("x"))),
    ).toEqual({ status: "failed" });
  });
});
