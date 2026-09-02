import { createServer } from "node:http";

// Proof harness stub for issue #1745, the invitation send cap.
//
// It serves the two upstreams the console talks to (Supabase Auth and the
// control plane) so the screenshots exercise the real built console against
// known answers rather than live tenant data, and so no email is sent anywhere.
//
// The invitation endpoint answers a fixed sequence, one status per POST:
// created, then capped, then counter-unavailable. That is the order a real
// caller meets them in, and it means one browser session and one page produce
// all three states with no restart between them.
//
// The two refusal bodies are byte-identical to what the control-plane handler
// produces (apps/control-plane/internal/accounts/http.go, asserted by
// TestInvitationHandler_CapAnswers429WithRetryAfterAndNoDimension and
// TestInvitationHandler_CounterOutageAnswers503), so what the console renders
// here is what it renders against the real server.

const PORT = Number(process.env.PROOF_STUB_PORT);
if (!Number.isInteger(PORT) || PORT <= 0) {
  throw new Error("PROOF_STUB_PORT is required and must be a port number");
}

const ACCOUNT_ID = "22222222-2222-2222-2222-222222222222";

const USER = {
  id: "11111111-1111-1111-1111-111111111111",
  aud: "authenticated",
  role: "authenticated",
  email: "proof@example.com",
  email_confirmed_at: "2026-01-01T00:00:00Z",
  phone: "",
  confirmed_at: "2026-01-01T00:00:00Z",
  app_metadata: { provider: "email", providers: ["email"] },
  user_metadata: {},
  identities: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

function jwt(payload) {
  const enc = (obj) => Buffer.from(JSON.stringify(obj)).toString("base64url");
  return `${enc({ alg: "HS256", typ: "JWT" })}.${enc(payload)}.proof-signature`;
}

const ACCESS_TOKEN = jwt({
  sub: USER.id,
  email: USER.email,
  role: "authenticated",
  tenant_id: ACCOUNT_ID,
  aud: "authenticated",
  iat: Math.floor(Date.now() / 1000),
  exp: Math.floor(Date.now() / 1000) + 60 * 60 * 24,
});

const SESSION = {
  access_token: ACCESS_TOKEN,
  token_type: "bearer",
  expires_in: 86400,
  expires_at: Math.floor(Date.now() / 1000) + 86400,
  refresh_token: "proof-refresh-token",
  user: USER,
};

const VIEWER = {
  user: { id: USER.id, email: USER.email, email_verified: true },
  current_account: {
    id: ACCOUNT_ID,
    display_name: "Northwind Analytics",
    account_type: "team",
    role: "owner",
  },
  memberships: [
    {
      account_id: ACCOUNT_ID,
      display_name: "Northwind Analytics",
      role: "owner",
      status: "active",
    },
  ],
  permissions: ["members.invite", "members.manage", "analytics.view"],
};

const ROSTER = {
  members: [
    {
      user_id: USER.id,
      email: USER.email,
      role: "owner",
      status: "active",
      joined_at: "2026-01-01T00:00:00Z",
    },
  ],
  invitations: [],
};

// One status per POST, in the order a caller meets them.
const INVITE_SEQUENCE = (process.env.PROOF_INVITE_SEQUENCE ?? "201,429,503")
  .split(",")
  .map((value) => Number(value.trim()));
let inviteCall = 0;

// Deliberately not a plausible-looking token. It is bearer-equivalent in the
// real product, it lands in a screenshot here, and a proof artifact must carry
// nothing that could be mistaken for a live credential.
const FAKE_TOKEN = "PROOF-FIXTURE-NOT-A-REAL-INVITATION-TOKEN";

function invitationResponse() {
  const status = INVITE_SEQUENCE[Math.min(inviteCall, INVITE_SEQUENCE.length - 1)];
  inviteCall += 1;
  if (status === 429) {
    return [
      429,
      {
        error: "invitation limit reached, try again in 5 minutes",
        code: "invitation_rate_limited",
      },
      { "retry-after": "300" },
    ];
  }
  if (status === 503) {
    return [
      503,
      {
        error: "invitations are temporarily unavailable, please try again shortly",
        code: "invitation_unavailable",
      },
      { "retry-after": "3600" },
    ];
  }
  return [
    201,
    {
      id: "33333333-3333-3333-3333-333333333333",
      email: "teammate@example.com",
      role: "member",
      token: FAKE_TOKEN,
      expires_at: "2026-09-05T00:00:00Z",
      delivered: true,
      delivery: "sent",
    },
    {},
  ];
}

// Lets a capture run start from the top of the sequence without restarting the
// console, so a rerun is deterministic rather than continuing where the last
// one stopped.
function resetSequence() {
  inviteCall = 0;
  return [200, { reset: true }, {}];
}

const ROUTES = new Map([
  ["POST /proof/reset", resetSequence],
  ["GET /auth/v1/user", () => [200, USER, {}]],
  ["POST /auth/v1/token", () => [200, SESSION, {}]],
  ["GET /api/v1/viewer", () => [200, VIEWER, {}]],
  ["GET /api/v1/accounts/current/members", () => [200, ROSTER, {}]],
  [
    "GET /api/v1/accounts/current/profile",
    () => [200, { owner_name: "Proof Owner" }, {}],
  ],
  ["POST /api/v1/accounts/current/invitations", invitationResponse],
]);

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", `http://127.0.0.1:${PORT}`);
  const key = `${req.method} ${url.pathname}`;
  const handler = ROUTES.get(key);
  const [status, body, headers] = handler
    ? handler()
    : [404, { error: "not found on the proof stub: " + key }, {}];
  const payload = JSON.stringify(body);
  console.log(`${key} -> ${status}`);
  res.writeHead(status, {
    ...headers,
    "content-type": "application/json",
    "content-length": Buffer.byteLength(payload),
  });
  res.end(payload);
});

// The request body is never read, so drain it rather than leaving the socket
// half-consumed on a POST.
server.on("request", (req) => req.resume());

server.listen(PORT, "0.0.0.0", () => {
  console.log(`proof stub listening on ${PORT}`);
});
