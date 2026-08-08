// Stand-in for everything apps/agent-console talks to: Supabase Auth (GoTrue)
// and edge-api. One process, stdlib http only, no dependencies.
//
// Why this exists: capturing the task console's states needs the console to
// reach a session and a task list. Doing that against live Supabase and a
// live edge-api means real credentials, real rows, and a running box, none of
// which a screenshot of a client-side state machine actually needs. The same
// throwaway stub was written and discarded three times before this file, so
// it is committed.
//
// Scope: only the routes this app calls. Password sign-in and session
// verification for GoTrue (see lib/supabase/{browser,server}.ts and
// middleware.ts), the feature gate (lib/edge-api/gate.ts), and the task
// lifecycle (lib/edge-api/tasks.ts). No signature checking anywhere: this
// process is both ends of the trust relationship for one capture run, and
// nothing it mints leaves the machine.
//
// Scenario control lives in POST /__control, so a capture script drives the
// console's states without this file knowing about any particular scenario.
// See README.md.
//
// ponytail: stdlib http, no framework, no route table. A dozen routes do not
// need one.

import http from "node:http";
import crypto from "node:crypto";

const PORT = Number(process.env.STUB_PORT || 4010);

// Fixed, obviously fake identities. Not credentials: no real system accepts
// any of these, and the token below is unsigned.
const USER_ID = "00000000-0000-4000-8000-000000000001";
const TENANT_ID = "00000000-0000-4000-8000-000000000002";
const EMAIL = "agent-console-harness@example.invalid";

/*
 * Mutable scenario state.
 *
 * `listFailures` is the reason the control endpoint exists at all: the
 * console's poll backoff and its give-up threshold are only observable when
 * GET /v1/agent/tasks fails a controlled number of times in a row.
 */
const state = {
  /** Tasks GET /v1/agent/tasks returns, newest first. */
  tasks: [],
  /** Remaining consecutive list requests to answer with a 503. */
  listFailures: 0,
  /** Status a task created through POST /v1/agent/tasks starts in. */
  createStatus: "queued",
  /**
   * Status transitions applied to a task the next time it is listed, keyed by
   * task id: ["running", "succeeded"] moves it one step per list request.
   * Models the server advancing a task while the console polls.
   */
  advanceOnList: {},
  /** Gate value GET /v1/featuregate reports for ENABLE_COWORK. */
  coworkEnabled: true,
};

function base64url(input) {
  return Buffer.from(input).toString("base64url");
}

// Shaped like a GoTrue access token so @supabase/supabase-js can read `exp`
// off it and not immediately try to refresh. Unsigned by design.
function buildAccessToken() {
  const now = Math.floor(Date.now() / 1000);
  const header = { alg: "HS256", typ: "JWT" };
  const payload = {
    sub: USER_ID,
    email: EMAIL,
    aud: "authenticated",
    role: "authenticated",
    tenant_id: TENANT_ID,
    iat: now,
    exp: now + 3600,
  };
  return [
    base64url(JSON.stringify(header)),
    base64url(JSON.stringify(payload)),
    "stub-signature-not-verified",
  ].join(".");
}

function userObject() {
  const now = new Date().toISOString();
  return {
    id: USER_ID,
    aud: "authenticated",
    role: "authenticated",
    email: EMAIL,
    email_confirmed_at: now,
    app_metadata: { provider: "email" },
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

/**
 * Fills in every field lib/edge-api/tasks.ts decodes, so a caller only has to
 * state what it cares about. `status` is passed straight through without
 * validation: handing it something outside the wire set is how the "unknown"
 * row gets exercised.
 */
function makeTask(partial = {}) {
  const now = new Date().toISOString();
  return {
    id: partial.id ?? crypto.randomUUID(),
    pack: partial.pack ?? "coding-pack",
    instructions: partial.instructions ?? "",
    status: partial.status ?? "queued",
    engine_session_ref: partial.engine_session_ref ?? "",
    result_summary_ref: partial.result_summary_ref ?? "",
    error_message: partial.error_message ?? "",
    created_at: partial.created_at ?? now,
    updated_at: partial.updated_at ?? now,
    started_at: partial.started_at ?? null,
    finished_at: partial.finished_at ?? null,
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
  res.writeHead(status, {
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(payload),
  });
  res.end(payload);
}

/**
 * Parses a request body as JSON, tolerating an empty body as `{}`. On
 * malformed input this sends a 400 itself and returns `ok: false`, so every
 * call site can bail out with `if (!parsed.ok) return;` instead of letting
 * `JSON.parse` throw inside an async handler, which Node has no default
 * catch for and which previously took the whole process down.
 */
function parseJsonBody(res, body) {
  try {
    const value = body ? JSON.parse(body) : {};
    if (value === null || Array.isArray(value) || typeof value !== "object") {
      sendJson(res, 400, { message: "stub: request body must be a JSON object" });
      return { ok: false, value: undefined };
    }
    return { ok: true, value };
  } catch {
    sendJson(res, 400, { message: "stub: request body is not valid JSON" });
    return { ok: false, value: undefined };
  }
}

/** Applies one pending transition per listed task, then hands back the list. */
function listTasks() {
  return state.tasks.map((task) => {
    const queue = state.advanceOnList[task.id];
    if (!queue || queue.length === 0) {
      return task;
    }
    const nextStatus = queue.shift();
    const advanced = { ...task, status: nextStatus, updated_at: new Date().toISOString() };
    state.tasks = state.tasks.map((t) => (t.id === task.id ? advanced : t));
    return advanced;
  });
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  const body = req.method === "POST" || req.method === "PUT" ? await readBody(req) : "";

  // The browser calls this origin cross-origin (console on :3000, stub on
  // :4010), so every response needs CORS and a preflight needs a 204. Real
  // GoTrue and edge-api both serve that way.
  res.setHeader("Access-Control-Allow-Origin", req.headers.origin || "*");
  res.setHeader("Access-Control-Allow-Methods", "GET,POST,OPTIONS");
  res.setHeader(
    "Access-Control-Allow-Headers",
    req.headers["access-control-request-headers"] || "*",
  );
  res.setHeader("Access-Control-Allow-Credentials", "true");
  if (req.method === "OPTIONS") {
    res.writeHead(204);
    res.end();
    return;
  }

  let note = "";

  // --- scenario control -------------------------------------------------
  if (url.pathname === "/__control" && req.method === "POST") {
    const parsedPatch = parseJsonBody(res, body);
    if (!parsedPatch.ok) return;
    const patch = parsedPatch.value;
    if (Array.isArray(patch.tasks)) {
      state.tasks = patch.tasks.map(makeTask);
    }
    if (typeof patch.listFailures === "number") {
      state.listFailures = patch.listFailures;
    }
    if (typeof patch.createStatus === "string") {
      state.createStatus = patch.createStatus;
    }
    if (patch.advanceOnList && typeof patch.advanceOnList === "object") {
      state.advanceOnList = { ...patch.advanceOnList };
    }
    if (typeof patch.coworkEnabled === "boolean") {
      state.coworkEnabled = patch.coworkEnabled;
    }
    sendJson(res, 200, { ok: true, state });
    return;
  }

  if (url.pathname === "/__control" && req.method === "GET") {
    sendJson(res, 200, state);
    return;
  }

  // --- GoTrue -----------------------------------------------------------
  if (url.pathname === "/auth/v1/token" && req.method === "POST") {
    // Credentials are not checked. Nothing here grants access to anything.
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

  // --- edge-api ---------------------------------------------------------
  if (url.pathname === "/v1/featuregate" && req.method === "GET") {
    sendJson(res, 200, { gates: { ENABLE_COWORK: state.coworkEnabled } });
    return;
  }

  if (url.pathname === "/v1/agent/tasks" && req.method === "GET") {
    if (state.listFailures > 0) {
      state.listFailures -= 1;
      note = ` (injected failure, ${state.listFailures} left)`;
      log(req, url, 503, note);
      sendJson(res, 503, { error: { message: "stub: list unavailable" } });
      return;
    }
    sendJson(res, 200, { tasks: listTasks() });
    log(req, url, 200, ` (${state.tasks.length} tasks)`);
    return;
  }

  if (url.pathname === "/v1/agent/tasks" && req.method === "POST") {
    const parsedPayload = parseJsonBody(res, body);
    if (!parsedPayload.ok) return;
    const payload = parsedPayload.value;
    const task = makeTask({
      pack: payload.pack,
      instructions: payload.instructions ?? "",
      status: state.createStatus,
    });
    state.tasks = [task, ...state.tasks];
    sendJson(res, 201, task);
    log(req, url, 201, ` (id ${task.id})`);
    return;
  }

  const cancel = url.pathname.match(/^\/v1\/agent\/tasks\/([^/]+)\/cancel$/);
  if (cancel && req.method === "POST") {
    const id = decodeURIComponent(cancel[1]);
    const existing = state.tasks.find((t) => t.id === id);
    if (!existing) {
      sendJson(res, 404, { error: { message: "stub: no such task" } });
      return;
    }
    const cancelled = {
      ...existing,
      status: "cancelled",
      finished_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    state.tasks = state.tasks.map((t) => (t.id === id ? cancelled : t));
    delete state.advanceOnList[id];
    sendJson(res, 200, cancelled);
    return;
  }

  const detail = url.pathname.match(/^\/v1\/agent\/tasks\/([^/]+)$/);
  if (detail && req.method === "GET") {
    const existing = state.tasks.find((t) => t.id === decodeURIComponent(detail[1]));
    if (!existing) {
      sendJson(res, 404, { error: { message: "stub: no such task" } });
      return;
    }
    sendJson(res, 200, existing);
    return;
  }

  log(req, url, 404, "");
  sendJson(res, 404, { message: `no stub route for ${req.method} ${url.pathname}` });
});

function log(req, url, status, suffix) {
  console.log(`[stub] ${req.method} ${url.pathname} -> ${status}${suffix}`);
}

server.listen(PORT, "127.0.0.1", () => {
  console.log(`[stub] listening on 127.0.0.1:${PORT}`);
});
