/**
 * console-docs-contract.test.ts
 *
 * Guards the console's API reference (`/console/docs`, issue #1179) against
 * the three ways a generated docs page goes quietly wrong.
 *
 * 1. The page is generated from `packages/openai-contract/`, which lives
 *    outside this app. If the line scan that reads the spec stops matching
 *    after a regeneration, the page renders an empty table and still returns
 *    200. Pinning the extracted operation count against the spec's own
 *    `x-hive-status:` annotation count makes that a red test rather than an
 *    empty page nobody notices.
 * 2. The quickstart names a host and a base path. Both are written down
 *    elsewhere as ground truth, and both have moved before, so they are
 *    asserted against those files instead of trusted.
 * 3. The whole reason the page exists is that the shell's Documentation link
 *    pointed off-product at hivegpt.io on every console page. Nothing stops
 *    that from being reintroduced except a check.
 */
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import {
  API_BASE_URL,
  OPENAPI_SPEC_PATH,
  STATUS_META,
  buildEndpointSections,
  diffSpecAgainstMatrix,
  endpointFamily,
  extractServerPrefix,
  extractSpecOperations,
  loadSpecOperations,
  loadSupportMatrix,
  parseSupportMatrix,
} from "@/lib/api-contract";

const APP_ROOT = resolve(__dirname, "../..");
const REPO_ROOT = resolve(APP_ROOT, "../..");

describe("support matrix", () => {
  it("parses the real matrix and carries its own generation date", () => {
    const matrix = loadSupportMatrix();

    expect(matrix.endpoints.length).toBeGreaterThan(0);
    // Printed on the page, so a matrix that lost its date would silently
    // render "generated unknown" instead of failing here.
    expect(matrix.generated).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    for (const endpoint of matrix.endpoints) {
      expect(endpoint.path.startsWith("/v1/")).toBe(true);
      expect(endpoint.method).toBe(endpoint.method.toUpperCase());
    }
  });

  it("rejects a matrix whose endpoints are unusable rather than rendering an empty table", () => {
    expect(() => parseSupportMatrix('{"version":"1"}')).toThrow(/endpoints/);
    expect(() =>
      parseSupportMatrix('{"endpoints":[{"method":"GET"}]}'),
    ).toThrow(/method, path or status/);
  });

  it("keeps every status in a section, including one the console does not describe", () => {
    const sections = buildEndpointSections({
      version: "test",
      generated: "2026-01-01",
      endpoints: [
        { method: "POST", path: "/v1/chat/completions", status: "supported_now", phase: 1, notes: "" },
        { method: "GET", path: "/v1/models", status: "supported_now", phase: 1, notes: "" },
        { method: "GET", path: "/v1/moonshot", status: "invented_status", phase: null, notes: "" },
      ],
    });

    const counted = sections.reduce((total, section) => total + section.count, 0);
    expect(counted).toBe(3);
    expect(sections.map((section) => section.meta.status)).toContain("invented_status");

    const supported = sections.find((s) => s.meta.status === "supported_now");
    expect(supported?.families.map((family) => family.name)).toEqual(["chat", "models"]);
  });

  it("derives a family from the first segment after the base path", () => {
    expect(endpointFamily("/v1/chat/completions")).toBe("chat");
    expect(endpointFamily("/v1/models")).toBe("models");
    expect(endpointFamily("/v1")).toBe("v1");
  });
});

describe("openapi spec extraction", () => {
  it("finds exactly the operations the generator annotated", () => {
    const spec = readFileSync(OPENAPI_SPEC_PATH, "utf8");
    // The generator stamps x-hive-status on every operation it publishes
    // (packages/openai-contract/scripts/sync_hive_contract.py), so counting
    // those annotations is an independent count of the same set.
    const annotated = spec.split("\n").filter((line) =>
      /^ {6}x-hive-status:/.test(line),
    ).length;

    const operations = loadSpecOperations();

    expect(annotated).toBeGreaterThan(0);
    expect(operations.length).toBe(annotated);
    expect(operations.every((op) => op.status !== null)).toBe(true);
    expect(operations.every((op) => op.operation.includes(" /v1/"))).toBe(true);
  });

  it("reads the server prefix from the spec instead of assuming it", () => {
    expect(extractServerPrefix(readFileSync(OPENAPI_SPEC_PATH, "utf8"))).toBe("/v1");
    expect(extractServerPrefix("servers:\n- url: /v2\n")).toBe("/v2");
    expect(extractServerPrefix("openapi: 3.0.0\n")).toBe("");
  });

  it("scans a path block without being fooled by nested keys", () => {
    const spec = [
      "servers:",
      "- url: /v1",
      "paths:",
      "  /chat/completions:",
      "    post:",
      "      operationId: createChatCompletion",
      "      x-hive-status: supported_now",
      "      responses:",
      "        get: not-a-method",
      "  /models:",
      "    get:",
      "      x-hive-status: supported_now",
      "components:",
      "  schemas:",
      "    get:",
      "",
    ].join("\n");

    expect(extractSpecOperations(spec)).toEqual([
      { operation: "POST /v1/chat/completions", status: "supported_now" },
      { operation: "GET /v1/models", status: "supported_now" },
    ]);
  });
});

describe("spec against matrix", () => {
  it("reports each kind of disagreement rather than picking a side", () => {
    const matrix = {
      version: "test",
      generated: "2026-01-01",
      endpoints: [
        { method: "POST", path: "/v1/chat/completions", status: "supported_now", phase: 1, notes: "" },
        { method: "POST", path: "/v1/rag/chat", status: "supported_now", phase: 1, notes: "" },
        { method: "GET", path: "/v1/models", status: "out_of_scope", phase: null, notes: "" },
      ],
    };
    const spec = [
      { operation: "POST /v1/chat/completions", status: "supported_now" },
      { operation: "GET /v1/models", status: "supported_now" },
      { operation: "DELETE /v1/files/{file_id}", status: "supported_now" },
    ];

    expect(diffSpecAgainstMatrix(spec, matrix)).toEqual([
      {
        operation: "DELETE /v1/files/{file_id}",
        kind: "missing_from_matrix",
        detail: "declared in the OpenAPI spec, unclassified in the support matrix",
      },
      {
        operation: "GET /v1/models",
        kind: "status_mismatch",
        detail:
          'the spec annotates it "supported_now", the support matrix classifies it "out_of_scope"',
      },
      {
        operation: "POST /v1/rag/chat",
        kind: "missing_from_spec",
        detail:
          'classified "supported_now" in the support matrix, absent from the OpenAPI spec',
      },
    ]);
  });

  it("has no unclassified spec operation, and no status mismatch in an unexpected direction", () => {
    const disagreements = diffSpecAgainstMatrix(
      loadSpecOperations(),
      loadSupportMatrix(),
    );

    // Matrix-only entries are expected and are rendered on the page: the
    // generator drops out-of-scope operations from the spec on purpose, and
    // Hive's own endpoints were never in the upstream OpenAI document the
    // spec is built from.
    expect(
      disagreements.filter((entry) => entry.kind === "missing_from_matrix"),
    ).toEqual([]);

    // Status mismatches are not expected, and today there is exactly one
    // kind: generated/hive-openapi.yaml was produced from an older matrix and
    // still annotates a set of live endpoints planned_for_launch. That is a
    // contract-package defect, reported on the docs page and in issue #1179,
    // not something the console can fix. A mismatch in any other direction is
    // new and must not arrive silently, so the direction is pinned rather
    // than the count.
    for (const entry of disagreements) {
      if (entry.kind === "status_mismatch") {
        expect(entry.detail).toBe(
          'the spec annotates it "planned_for_launch", the support matrix classifies it "supported_now"',
        );
      }
    }
  });
});

describe("quickstart facts", () => {
  it("names a host the tunnel actually serves, on the spec's own base path", () => {
    const base = new URL(API_BASE_URL);
    const ingress: { config: { ingress: { hostname?: string }[] } } = JSON.parse(
      readFileSync(resolve(REPO_ROOT, "deploy/cloudflare/tunnel-ingress.json"), "utf8"),
    );
    const hostnames = ingress.config.ingress
      .map((rule) => rule.hostname)
      .filter((hostname): hostname is string => typeof hostname === "string");

    expect(hostnames).toContain(base.hostname);
    expect(base.pathname).toBe(
      extractServerPrefix(readFileSync(OPENAPI_SPEC_PATH, "utf8")),
    );
    expect(base.protocol).toBe("https:");
  });

  it("describes every status the console renders", () => {
    expect(new Set(STATUS_META.map((meta) => meta.status)).size).toBe(
      STATUS_META.length,
    );
    for (const meta of STATUS_META) {
      expect(meta.label.length).toBeGreaterThan(0);
      expect(meta.meaning.length).toBeGreaterThan(0);
    }
  });
});

describe("documentation links stay in product", () => {
  it("no console surface links out to hivegpt.io", () => {
    for (const file of [
      "components/app-shell/console-shell.tsx",
      "app/console/page.tsx",
    ]) {
      const source = readFileSync(resolve(APP_ROOT, file), "utf8");
      expect(source).not.toMatch(/href=["']https:\/\/hivegpt\.io/);
      expect(source).toContain('"/console/docs"');
    }
  });
});
