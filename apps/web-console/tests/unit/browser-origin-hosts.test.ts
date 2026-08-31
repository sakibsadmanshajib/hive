/**
 * browser-origin-hosts.test.ts
 *
 * Nothing the browser downloads may name a compose-internal hostname as an
 * origin, and the voice client in particular must keep deriving its origin the
 * way the rest of the chat surface derives its API origin.
 *
 * The bug this guards (issue #1562): a container hostname such as
 * `edge-api:8080` resolves only inside the compose network. Server to server it
 * is correct and is what the chat container's audio configuration uses. Shipped
 * to a browser it is unreachable on every deployment, local, demo and
 * customer-hosted alike, which is this repository's dominant defect class: a
 * surface configured with a value that is only valid in a context the user is
 * never in.
 *
 * The service list is read out of `deploy/docker/docker-compose.yml` rather
 * than written down here, for the same reason `control-plane-host.test.ts`
 * reads `deploy/cloudflare/tunnel-ingress.json`: a hardcoded roster stops
 * covering the service added after it was written, and the day it stops
 * covering something is silent.
 *
 * Scope is `vendor/open-webui/src`, the chat front end, which is the surface
 * the issue is about and is entirely browser code. Open WebUI builds through
 * `adapter-static` and `deploy/docker/Dockerfile.open-webui` replaces
 * `/app/build` with the result, so every file under that tree is downloaded by
 * a browser and none of it runs on a server. There is therefore no legitimate
 * reason for an internal origin to appear in it.
 *
 * File to file, no network and no credentials, so it runs in the required unit
 * check.
 */

import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, extname, relative, resolve } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "../../../..");
const COMPOSE_PATH = resolve(REPO_ROOT, "deploy/docker/docker-compose.yml");
const CHAT_FRONTEND_SRC = resolve(REPO_ROOT, "vendor/open-webui/src");
const AUDIO_CLIENT = resolve(CHAT_FRONTEND_SRC, "lib/apis/audio/index.ts");
const CONSTANTS = resolve(CHAT_FRONTEND_SRC, "lib/constants.ts");

const SCANNED_EXTENSIONS: ReadonlySet<string> = new Set([
  ".ts",
  ".js",
  ".mjs",
  ".svelte",
  ".html",
  ".json",
]);

/**
 * The names docker compose resolves on its own network, and nowhere else.
 *
 * Only the `services:` block. The top-level `volumes:` block below it is
 * indented identically, and sweeping it in would add names like `owui-data`
 * that mean nothing as hostnames, so the walk stops at the next top-level key.
 */
function composeServiceNames(): string[] {
  const compose = readFileSync(COMPOSE_PATH, "utf8");
  const lines = compose.split("\n");
  const start = lines.findIndex((line) => /^services:\s*$/.test(line));
  expect(start, "docker-compose.yml has no services block").toBeGreaterThan(-1);

  const names: string[] = [];
  for (const line of lines.slice(start + 1)) {
    if (/^[A-Za-z]/.test(line)) break; // next top-level key
    const match = /^ {2}([a-z0-9][a-z0-9_.-]*):\s*(#.*)?$/.exec(line);
    if (match) names.push(match[1]);
  }
  expect(names.length, "no compose services parsed").toBeGreaterThan(5);
  return names;
}

function walk(root: string): string[] {
  const found: string[] = [];
  const visit = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      if (entry === "node_modules" || entry === ".svelte-kit") continue;
      const full = resolve(dir, entry);
      if (statSync(full).isDirectory()) {
        visit(full);
        continue;
      }
      if (SCANNED_EXTENSIONS.has(extname(entry))) found.push(full);
    }
  };
  visit(root);
  return found;
}

describe("browser-shipped chat source names no compose-internal origin", () => {
  it("has a chat front end to scan", () => {
    // Guards the guard. A moved or renamed tree would make every assertion
    // below vacuously true over an empty file list.
    expect(walk(CHAT_FRONTEND_SRC).length).toBeGreaterThan(100);
  });

  it("never writes an internal service hostname as a URL origin", () => {
    const services = composeServiceNames();
    // `http://edge-api:8080`, `https://litellm/`, `http://open-webui:8080/v1`.
    // Anchored on the scheme and closed on a port or path separator, so prose
    // that merely mentions a service by name (there is a lot of it, and it is
    // useful) is untouched, and only a value shaped like an origin matches.
    const internalOrigin = new RegExp(
      `https?://(${services.map((s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})(:\\d+)?(?=[/'"\`\\s?#]|$)`,
      "gi",
    );

    const offenders: string[] = [];
    for (const file of walk(CHAT_FRONTEND_SRC)) {
      const text = readFileSync(file, "utf8");
      for (const hit of text.matchAll(internalOrigin)) {
        offenders.push(`${relative(REPO_ROOT, file)}: ${hit[0]}`);
      }
    }

    expect(
      offenders,
      "a compose-internal hostname resolves only inside the compose network, so a browser cannot reach it (#1562)",
    ).toEqual([]);
  });
});

describe("the voice client derives its origin the way the rest of the app does", () => {
  it("routes every audio call through AUDIO_API_BASE_URL", () => {
    const source = readFileSync(AUDIO_CLIENT, "utf8");
    const targets = [...source.matchAll(/fetch\(\s*`([^`]*)`/g)].map(
      (m) => m[1],
    );

    // Speech synthesis, transcription, the voice roster and the audio config
    // are four separate calls; fewer than that means this stopped reading the
    // file it thinks it is reading.
    expect(targets.length, "no audio fetch targets found").toBeGreaterThanOrEqual(4);
    for (const target of targets) {
      expect(
        target.startsWith("${AUDIO_API_BASE_URL}"),
        `audio call to ${target} does not go through AUDIO_API_BASE_URL`,
      ).toBe(true);
    }
  });

  it("defines AUDIO_API_BASE_URL as same-origin, from WEBUI_BASE_URL", () => {
    const source = readFileSync(CONSTANTS, "utf8");

    // Same derivation as WEBUI_API_BASE_URL, OPENAI_API_BASE_URL and the rest:
    // one origin for the whole surface, empty in a production build so every
    // path is relative and resolves against whatever Caddy front served the
    // page. That is what makes it correct for local development, for the demo
    // deployment and for a customer-hosted install without any of them being
    // named anywhere.
    expect(source).toMatch(
      /export const AUDIO_API_BASE_URL\s*=\s*`\$\{WEBUI_BASE_URL\}\/api\/v1\/audio`/,
    );
    expect(source).toMatch(
      /export const WEBUI_BASE_URL\s*=\s*browser\s*\?/,
    );
  });
});
