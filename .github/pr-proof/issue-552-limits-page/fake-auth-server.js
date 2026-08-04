// Local Supabase Auth (GoTrue) stand-in for the issue-552/#683 prod-container
// proof capture. Implements only the two endpoints the real @supabase/ssr
// client calls on this page's path: password sign-in and session
// verification. No cryptographic signature checking: this process is both
// ends of the "trust" relationship for the duration of one capture run, and
// nothing outside this script ever sees the token. See README.md for why a
// stand-in is used instead of live Supabase Cloud.
//
// ponytail: stdlib http only, no framework, two routes do not need one.

const http = require("http");
const crypto = require("crypto");

const PORT = Number(process.env.FAKE_AUTH_PORT || 4001);
const USER_ID = process.env.FAKE_AUTH_USER_ID || "3f6a6e40-2f0a-4b8a-9e2c-6b6a6f9a2b10";
const EMAIL = process.env.FAKE_AUTH_EMAIL || "proof552@example.invalid";
const TENANT_ID = process.env.FAKE_AUTH_TENANT_ID || "8b1e1c2a-9d3f-4a6b-8c5d-1e2f3a4b5c6d";

function base64url(input) {
  return Buffer.from(input).toString("base64url");
}

function buildAccessToken() {
  const header = { alg: "HS256", typ: "JWT" };
  const now = Math.floor(Date.now() / 1000);
  const payload = {
    sub: USER_ID,
    email: EMAIL,
    aud: "authenticated",
    role: "authenticated",
    tenant_id: TENANT_ID,
    iat: now,
    exp: now + 3600,
  };
  const signature = crypto.randomBytes(16).toString("base64url");
  return `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(payload))}.${signature}`;
}

function userObject() {
  const now = new Date().toISOString();
  return {
    id: USER_ID,
    aud: "authenticated",
    role: "authenticated",
    email: EMAIL,
    email_confirmed_at: now,
    app_metadata: { provider: "email", hive_email_verified: true },
    user_metadata: {},
    created_at: now,
  };
}

function tokenResponse() {
  return {
    access_token: buildAccessToken(),
    token_type: "bearer",
    expires_in: 3600,
    expires_at: Math.floor(Date.now() / 1000) + 3600,
    refresh_token: crypto.randomBytes(24).toString("base64url"),
    user: userObject(),
  };
}

function readBody(req) {
  return new Promise((resolve) => {
    let raw = "";
    req.on("data", (chunk) => (raw += chunk));
    req.on("end", () => resolve(raw));
  });
}

function sendJson(res, status, body) {
  const payload = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload) });
  res.end(payload);
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  console.log(`[fake-auth] ${req.method} ${url.pathname}${url.search}`);

  // The browser-side createBrowserClient calls this origin cross-origin
  // (the console app is served from caddy-console, this stand-in listens on
  // a different host:port), so every response needs CORS headers, and a
  // preflight OPTIONS needs an explicit 204. Real GoTrue serves the same way.
  res.setHeader("Access-Control-Allow-Origin", req.headers.origin || "*");
  res.setHeader("Access-Control-Allow-Methods", "GET,POST,OPTIONS");
  res.setHeader("Access-Control-Allow-Headers", req.headers["access-control-request-headers"] || "*");
  res.setHeader("Access-Control-Allow-Credentials", "true");
  if (req.method === "OPTIONS") {
    res.writeHead(204);
    res.end();
    return;
  }

  if (url.pathname === "/auth/v1/token" && req.method === "POST") {
    await readBody(req); // grant_type/password body is accepted, credentials are not checked
    sendJson(res, 200, tokenResponse());
    return;
  }

  if (url.pathname === "/auth/v1/user" && req.method === "GET") {
    if (!req.headers.authorization) {
      sendJson(res, 401, { message: "missing bearer token" });
      return;
    }
    sendJson(res, 200, userObject());
    return;
  }

  if (url.pathname === "/auth/v1/logout" && req.method === "POST") {
    res.writeHead(204);
    res.end();
    return;
  }

  sendJson(res, 404, { message: `no stand-in route for ${req.method} ${url.pathname}` });
});

server.listen(PORT, "0.0.0.0", () => {
  console.log(`[fake-auth] listening on 0.0.0.0:${PORT}`);
});
