// Local control-plane stand-in for the issue-552/#683 prod-container proof
// capture. Serves only the routes the limits page's server code calls:
// GET /api/v1/viewer and GET+PUT .../api-keys/{id}/limits. Everything else
// 404s: the console layout's balance/budget calls are wrapped in
// Promise.allSettled and degrade to zero/null on failure, so they do not
// need a stand-in route to render the page.
//
// ponytail: stdlib http only, three routes do not need a framework.

const http = require("http");

const PORT = Number(process.env.FAKE_CONTROL_PLANE_PORT || 4002);
const ACCOUNT_ID = process.env.FAKE_CP_ACCOUNT_ID || "8b1e1c2a-9d3f-4a6b-8c5d-1e2f3a4b5c6d";
const USER_ID = process.env.FAKE_AUTH_USER_ID || "3f6a6e40-2f0a-4b8a-9e2c-6b6a6f9a2b10";
const EMAIL = process.env.FAKE_AUTH_EMAIL || "proof552@example.invalid";

// In-memory limits row, seeded with the same values the earlier next-dev
// capture (limits-after.png/.log) used, so the two proof captures read the
// same way at a glance.
let limits = {
  api_key_id: "",
  rpm: 60,
  tpm: 4000,
  tier_overrides: {},
};

function viewerResponse() {
  return {
    user: { id: USER_ID, email: EMAIL, email_verified: true },
    current_account: {
      id: ACCOUNT_ID,
      display_name: "Proof Workspace (prod container)",
      account_type: "individual",
      role: "owner",
    },
    memberships: [
      {
        account_id: ACCOUNT_ID,
        display_name: "Proof Workspace (prod container)",
        role: "owner",
        status: "active",
      },
    ],
    permissions: ["api_keys.read", "api_keys.write"],
  };
}

function readJsonBody(req) {
  return new Promise((resolve, reject) => {
    let raw = "";
    req.on("data", (chunk) => (raw += chunk));
    req.on("end", () => {
      if (!raw) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch (err) {
        reject(err);
      }
    });
  });
}

function sendJson(res, status, body) {
  const payload = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload) });
  res.end(payload);
}

const limitsPathPattern = /^\/api\/v1\/accounts\/current\/api-keys\/([^/]+)\/limits$/;

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  console.log(`[fake-control-plane] ${req.method} ${url.pathname}`);

  if (url.pathname === "/api/v1/viewer" && req.method === "GET") {
    sendJson(res, 200, viewerResponse());
    return;
  }

  const limitsMatch = url.pathname.match(limitsPathPattern);
  if (limitsMatch && req.method === "GET") {
    sendJson(res, 200, { ...limits, api_key_id: limitsMatch[1] });
    return;
  }

  if (limitsMatch && req.method === "PUT") {
    try {
      const body = await readJsonBody(req);
      limits = {
        api_key_id: limitsMatch[1],
        rpm: typeof body.rpm === "number" ? body.rpm : limits.rpm,
        tpm: typeof body.tpm === "number" ? body.tpm : limits.tpm,
        tier_overrides: body.tier_overrides ?? {},
      };
      sendJson(res, 200, limits);
    } catch {
      sendJson(res, 400, { message: "invalid JSON body" });
    }
    return;
  }

  // Balance/budget-threshold and anything else: 404. The console layout
  // wraps these in Promise.allSettled and renders zero/null on failure.
  sendJson(res, 404, { message: `no stand-in route for ${req.method} ${url.pathname}` });
});

server.listen(PORT, "0.0.0.0", () => {
  console.log(`[fake-control-plane] listening on 0.0.0.0:${PORT}`);
});
