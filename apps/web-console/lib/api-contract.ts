/**
 * api-contract.ts
 *
 * Reads the generated OpenAPI contract and its support matrix so the console's
 * docs page is generated from them rather than hand written.
 *
 * The contract lives in `packages/openai-contract/`, outside this app. It is
 * read from disk instead of imported for two reasons: the spec is 1.1 MB of
 * YAML, which no bundler loader in this app can turn into a module, and
 * copying either file into `apps/web-console/` would create a second version
 * of a generated artefact, which is exactly the drift that generating from a
 * spec exists to prevent. Both `deploy/docker/Dockerfile.web-console` and
 * `deploy/docker/Dockerfile.web-console.prod` COPY the package into the image
 * for this reason; a route that reads it will 500 in any image that does not.
 *
 * Every fact rendered on `/console/docs` comes from one of these two files.
 * Where they disagree, the disagreement is reported on the page rather than
 * resolved silently in favour of one of them.
 */
import { readFileSync } from "node:fs";
import path from "node:path";

const CONTRACT_ROOT = path.resolve(
  process.cwd(),
  "..",
  "..",
  "packages",
  "openai-contract",
);

export const OPENAPI_SPEC_PATH = path.join(
  CONTRACT_ROOT,
  "generated",
  "hive-openapi.yaml",
);

export const SUPPORT_MATRIX_PATH = path.join(
  CONTRACT_ROOT,
  "matrix",
  "support-matrix.json",
);

/**
 * Public base URL of the gateway's OpenAI-compatible API.
 *
 * `api-hive.scubed.co` is edge-api in `deploy/cloudflare/tunnel-ingress.json`,
 * which that file's own header calls the single source of truth for which hive
 * hostnames are public. `/v1` is the `servers[0].url` the generated spec
 * declares. `tests/unit/console-docs-contract.test.ts` asserts both halves
 * still hold, so a hostname move fails a check instead of leaving a quickstart
 * that quietly points at nothing.
 */
export const API_BASE_URL = "https://api-hive.scubed.co/v1";

/** Public, unauthenticated route this app serves the raw spec on. */
export const OPENAPI_ROUTE = "/api/openapi.yaml";

export interface MatrixEndpoint {
  method: string;
  path: string;
  status: string;
  phase: number | null;
  notes: string;
}

export interface SupportMatrix {
  version: string;
  /** The matrix's own generation date, printed on the page so staleness shows. */
  generated: string;
  endpoints: MatrixEndpoint[];
}

export interface StatusMeta {
  status: string;
  label: string;
  meaning: string;
}

/**
 * Render order, and what each status actually means to an integrator. The
 * unsupported and out-of-scope statuses are on the page deliberately: docs
 * that list only the happy path let somebody build against an endpoint that
 * was never implemented.
 */
export const STATUS_META: readonly StatusMeta[] = [
  {
    status: "supported_now",
    label: "Supported now",
    meaning: "Implemented and served by the gateway today.",
  },
  {
    status: "planned_for_launch",
    label: "Planned for launch",
    meaning: "Not implemented yet. On the launch path, so do not build against it now.",
  },
  {
    status: "explicitly_unsupported_at_launch",
    label: "Not supported at launch",
    meaning:
      "Part of the OpenAI contract this gateway mirrors, deliberately not implemented here.",
  },
  {
    status: "out_of_scope",
    label: "Out of scope",
    meaning: "Not part of this gateway's surface and not planned.",
  },
];

const HTTP_METHODS = new Set([
  "get",
  "put",
  "post",
  "delete",
  "options",
  "head",
  "patch",
  "trace",
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Parses the support matrix, failing loudly on a shape it does not recognise.
 * A silently empty table would read as "this gateway supports nothing", which
 * is worse than an error page.
 */
export function parseSupportMatrix(raw: string): SupportMatrix {
  const parsed: unknown = JSON.parse(raw);
  if (!isRecord(parsed) || !Array.isArray(parsed.endpoints)) {
    throw new Error("support matrix has no endpoints array");
  }

  const endpoints: MatrixEndpoint[] = parsed.endpoints.map((entry, index) => {
    if (
      !isRecord(entry) ||
      typeof entry.method !== "string" ||
      typeof entry.path !== "string" ||
      typeof entry.status !== "string"
    ) {
      throw new Error(`support matrix endpoint ${index} is missing method, path or status`);
    }
    return {
      method: entry.method.toUpperCase(),
      path: entry.path,
      status: entry.status,
      phase: typeof entry.phase === "number" ? entry.phase : null,
      notes: typeof entry.notes === "string" ? entry.notes : "",
    };
  });

  return {
    version: typeof parsed.version === "string" ? parsed.version : "unknown",
    generated: typeof parsed.generated === "string" ? parsed.generated : "unknown",
    endpoints,
  };
}

export function loadSupportMatrix(): SupportMatrix {
  return parseSupportMatrix(readFileSync(SUPPORT_MATRIX_PATH, "utf8"));
}

/**
 * The prefix every path in the spec is served under, read from the spec's own
 * `servers` block rather than assumed. Matrix paths already carry it, spec
 * paths do not, and comparing them without it produces 164 phantom
 * disagreements.
 */
export function extractServerPrefix(spec: string): string {
  const match = /^servers:\n-\s*url:\s*(\S+)/m.exec(spec);
  return match ? match[1].replace(/\/$/, "") : "";
}

export interface SpecOperation {
  /** `METHOD /v1/path`. */
  operation: string;
  /** The `x-hive-status` the generator stamped on it, when it stamped one. */
  status: string | null;
}

/**
 * Every operation the generated spec declares, with the support status the
 * generator annotated it with.
 *
 * A line scan rather than a YAML parse: the only facts needed here are which
 * method/path pairs exist and what `x-hive-status` each carries, the file is
 * 29k lines, and adding a YAML dependency to render a table is not worth it.
 * Inside the `paths:` block a path key is the only thing indented two spaces,
 * a method key the only thing indented four, and the generator writes
 * `x-hive-status` at six. `tests/unit/console-docs-contract.test.ts` pins the
 * extracted count against the real file, which is what catches a future
 * regeneration that changes the formatting rather than letting the table
 * quietly empty out.
 */
export function extractSpecOperations(spec: string): SpecOperation[] {
  const prefix = extractServerPrefix(spec);
  const operations: SpecOperation[] = [];
  let inPaths = false;
  let currentPath: string | null = null;
  let current: SpecOperation | null = null;

  for (const line of spec.split("\n")) {
    if (!inPaths) {
      if (line === "paths:") {
        inPaths = true;
      }
      continue;
    }
    if (line.trim() === "") {
      continue;
    }
    if (/^\S/.test(line)) {
      break; // next top-level key; the paths block is over
    }

    const pathKey = /^ {2}(\/\S*):\s*$/.exec(line);
    if (pathKey) {
      currentPath = pathKey[1];
      current = null;
      continue;
    }

    const methodKey = /^ {4}([a-z]+):\s*$/.exec(line);
    if (methodKey && currentPath !== null && HTTP_METHODS.has(methodKey[1])) {
      current = {
        operation: `${methodKey[1].toUpperCase()} ${prefix}${currentPath}`,
        status: null,
      };
      operations.push(current);
      continue;
    }

    const statusKey = /^ {6}x-hive-status:\s*(\S+)\s*$/.exec(line);
    if (statusKey && current !== null) {
      current.status = statusKey[1];
    }
  }

  return operations;
}

export function loadSpecOperations(): SpecOperation[] {
  return extractSpecOperations(readFileSync(OPENAPI_SPEC_PATH, "utf8"));
}

export type DisagreementKind =
  /** The matrix classifies it; the spec does not describe it at all. */
  | "missing_from_spec"
  /** The spec describes it; the matrix does not classify it. */
  | "missing_from_matrix"
  /** Both have it, and they disagree about its support status. */
  | "status_mismatch";

export interface ContractDisagreement {
  /** `METHOD /v1/path`. */
  operation: string;
  kind: DisagreementKind;
  /** What the two files each say about it. */
  detail: string;
}

/**
 * Where the matrix and the generated spec do not agree. Reported on the page
 * instead of picked between, because every direction is a real finding: a
 * matrix row with no spec operation means the spec does not describe an
 * endpoint somebody may be told to call, a spec operation with no matrix row
 * means an endpoint nobody has classified, and a status mismatch means one of
 * the two is stale.
 */
export function diffSpecAgainstMatrix(
  specOperations: readonly SpecOperation[],
  matrix: SupportMatrix,
): ContractDisagreement[] {
  const inSpec = new Map(specOperations.map((entry) => [entry.operation, entry.status]));
  const inMatrix = new Map(
    matrix.endpoints.map((endpoint) => [
      `${endpoint.method} ${endpoint.path}`,
      endpoint.status,
    ]),
  );

  const disagreements: ContractDisagreement[] = [];
  for (const [operation, matrixStatus] of inMatrix) {
    if (!inSpec.has(operation)) {
      disagreements.push({
        operation,
        kind: "missing_from_spec",
        detail: `classified "${matrixStatus}" in the support matrix, absent from the OpenAPI spec`,
      });
      continue;
    }
    const specStatus = inSpec.get(operation) ?? null;
    if (specStatus !== null && specStatus !== matrixStatus) {
      disagreements.push({
        operation,
        kind: "status_mismatch",
        detail: `the spec annotates it "${specStatus}", the support matrix classifies it "${matrixStatus}"`,
      });
    }
  }
  for (const operation of inSpec.keys()) {
    if (!inMatrix.has(operation)) {
      disagreements.push({
        operation,
        kind: "missing_from_matrix",
        detail: "declared in the OpenAPI spec, unclassified in the support matrix",
      });
    }
  }

  return disagreements.sort((a, b) => a.operation.localeCompare(b.operation));
}

export interface EndpointFamily {
  name: string;
  endpoints: MatrixEndpoint[];
}

export interface EndpointSection {
  meta: StatusMeta;
  count: number;
  families: EndpointFamily[];
}

/** `/v1/chat/completions` -> `chat`. The first segment after the base path. */
export function endpointFamily(endpointPath: string): string {
  const segments = endpointPath.split("/").filter((segment) => segment !== "");
  return segments.length > 1 ? segments[1] : (segments[0] ?? "root");
}

/**
 * Groups the matrix into one section per status, each grouped by resource
 * family. A status the matrix carries but `STATUS_META` does not describe gets
 * its own section rather than being dropped, so an unrecognised classification
 * is visible on the page instead of vanishing from the counts.
 */
export function buildEndpointSections(matrix: SupportMatrix): EndpointSection[] {
  const known = new Set(STATUS_META.map((meta) => meta.status));
  const extra = [...new Set(matrix.endpoints.map((endpoint) => endpoint.status))]
    .filter((status) => !known.has(status))
    .sort()
    .map((status): StatusMeta => ({
      status,
      label: status,
      meaning: "Status present in the support matrix but not described by the console.",
    }));

  return [...STATUS_META, ...extra]
    .map((meta) => {
      const matching = matrix.endpoints.filter(
        (endpoint) => endpoint.status === meta.status,
      );
      const byFamily = new Map<string, MatrixEndpoint[]>();
      for (const endpoint of matching) {
        const family = endpointFamily(endpoint.path);
        const bucket = byFamily.get(family);
        if (bucket) {
          bucket.push(endpoint);
        } else {
          byFamily.set(family, [endpoint]);
        }
      }
      const families = [...byFamily.entries()]
        .map(([name, endpoints]) => ({
          name,
          endpoints: endpoints.sort(
            (a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method),
          ),
        }))
        .sort((a, b) => a.name.localeCompare(b.name));
      return { meta, count: matching.length, families };
    })
    .filter((section) => section.count > 0);
}
