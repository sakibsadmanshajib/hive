// @vitest-environment node
//
// A filesystem scan and a TypeScript parse, no DOM.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

import ts from "typescript";
import { describe, expect, it } from "vitest";

/**
 * Issue #494: console Server Components called a control-plane read with no
 * tolerance, so one non-2xx answer threw out of the component and tore down
 * the whole page tree. The fix routes those reads through lib/console/data.ts.
 * This guard is what makes a call site added later inherit that instead of
 * quietly reintroducing the crash.
 *
 * It parses rather than scanning lines. The first version of this guard did
 * scan lines, with a window around each match, and that window was itself a
 * hole: a bare read one line under a tolerated one inside the same
 * Promise.all read as tolerated. That is issue #494's own shape — the
 * original defect was a bare getBalance() two lines above a caught
 * getBudgetThreshold() in one Promise.all — so the guard would not have
 * caught the bug it exists to prevent. Asking the AST which call the
 * tolerance actually wraps is the only way to answer that correctly.
 */

const WEB_CONSOLE = join(__dirname, "..");
const CLIENT_MODULE = join(WEB_CONSOLE, "lib", "control-plane", "client.ts");

/**
 * Directories that render, plus the helpers they call. Route handlers count:
 * they are .ts rather than .tsx, and three of them already live under
 * app/console.
 */
const SCAN_ROOTS = ["app", "lib", "components"];

/** Not a call site: the seam itself, and the module the reads come from. */
const EXEMPT = [
  join("lib", "console", "data.ts"),
  join("lib", "control-plane", "client.ts"),
];

const SOURCE_EXTENSIONS = [".ts", ".tsx"];

/**
 * The reads, taken from the client module's own exports rather than from a
 * list kept by hand here. The hand-kept list had already drifted: it was
 * missing getInvoice and getInvoicePdfUrl, so a bare call to either passed.
 * Deriving it means a read added to the client module is covered the day it
 * lands.
 */
function controlPlaneReads(): string[] {
  const source = ts.createSourceFile(
    CLIENT_MODULE,
    readFileSync(CLIENT_MODULE, "utf8"),
    ts.ScriptTarget.Latest,
    true,
  );

  const reads: string[] = [];
  source.forEachChild((node) => {
    if (!ts.isFunctionDeclaration(node) || !node.name) {
      return;
    }
    const exported = node.modifiers?.some(
      (m) => m.kind === ts.SyntaxKind.ExportKeyword,
    );
    // get*/list* are the reads. The writes (create/update/delete/revoke/
    // dismiss/initiate/reconcile/set/upsert) are called from server actions
    // and event handlers that report their own failure to the customer, which
    // is a different problem from a render that cannot complete.
    if (exported && /^(get|list)[A-Z]/.test(node.name.text)) {
      reads.push(node.name.text);
    }
  });
  return reads.sort();
}

function sourceFilesUnder(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    if (entry === "node_modules" || entry === "__tests__") {
      continue;
    }
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...sourceFilesUnder(full));
      continue;
    }
    if (
      SOURCE_EXTENSIONS.some((ext) => entry.endsWith(ext)) &&
      !entry.includes(".test.") &&
      !entry.endsWith(".d.ts")
    ) {
      out.push(full);
    }
  }
  return out;
}

/** Is `node` inside a call to one of `names`, as an argument? */
function insideCallTo(node: ts.Node, names: string[]): boolean {
  for (let cur = node.parent; cur; cur = cur.parent) {
    if (ts.isCallExpression(cur) && ts.isIdentifier(cur.expression)) {
      if (names.includes(cur.expression.text) && cur.expression !== node) {
        return true;
      }
    }
  }
  return false;
}

/**
 * Is the read's promise handed to a .catch() in the same expression chain?
 *
 * In the render tree that handler must re-raise Next.js control flow, for the
 * same reason a try/catch must: every read on this list awaits cookies()
 * through getRequestContext, so every one of them can be handed a
 * DynamicServerError by the prerender pass, and `.catch((): [] => [])` would
 * answer it with a fabricated empty collection. Accepting a bare `.catch` was
 * how the first version of this guard blessed exactly what the seam it guards
 * forbids.
 */
function isCaught(node: ts.Node, mustRethrow: boolean): boolean {
  let cur: ts.Node = node;
  while (cur.parent) {
    const parent = cur.parent;
    if (
      ts.isPropertyAccessExpression(parent) &&
      parent.expression === cur &&
      parent.name.text === "catch"
    ) {
      if (!mustRethrow) {
        return true;
      }
      const call = parent.parent;
      if (!call || !ts.isCallExpression(call)) {
        return false;
      }
      const handler = call.arguments[0];
      return handler ? reRaises(handler) : false;
    }
    if (
      (ts.isPropertyAccessExpression(parent) || ts.isCallExpression(parent)) &&
      (parent as ts.PropertyAccessExpression | ts.CallExpression).expression ===
        cur
    ) {
      cur = parent;
      continue;
    }
    return false;
  }
  return false;
}

/**
 * In the render tree a try block only counts as tolerance when its catch
 * re-raises Next.js's own control flow. redirect(), notFound() and "this
 * route read cookies so it cannot be prerendered" are all signalled by
 * throwing; a catch that answers those with a fallback turns a framework
 * instruction into a fabricated result. `catch {}` with no binding cannot
 * rethrow anything at all.
 *
 * Route handlers are held to the weaker rule of merely catching. They are
 * never prerendered and render no tree, so there is no control flow of this
 * kind to preserve, and answering a failed read with a 500 or a redirect is
 * the correct thing for one to do. An uncaught read in a route handler is
 * still an offence.
 */
function insideTry(node: ts.Node, mustRethrow: boolean): boolean {
  for (let cur = node.parent; cur; cur = cur.parent) {
    if (!ts.isBlock(cur) || !cur.parent || !ts.isTryStatement(cur.parent)) {
      continue;
    }
    const tryStatement = cur.parent;
    if (tryStatement.tryBlock !== cur) {
      continue;
    }
    const clause = tryStatement.catchClause;
    if (!clause) {
      continue;
    }
    // The INNERMOST enclosing try is the one that handles this read. An outer
    // try that re-raises is no help when an inner catch has already swallowed
    // the throw, so this answers on the first one found and stops.
    return mustRethrow ? reRaises(clause.block) : true;
  }
  return false;
}

const reads = controlPlaneReads();

/**
 * Which local identifiers in this file refer to a control-plane read.
 *
 * `import { getBalance as gb }` and `import * as cp` both hide the read from a
 * name check against the import's original spelling, so the binding is
 * resolved rather than assumed: aliases map back to what they alias, and a
 * namespace import is remembered so `cp.getBalance()` is recognised too.
 */
interface ReadBindings {
  locals: Set<string>;
  namespaces: Set<string>;
}

function readBindings(source: ts.SourceFile): ReadBindings {
  const locals = new Set<string>();
  const namespaces = new Set<string>();

  source.forEachChild((node) => {
    if (!ts.isImportDeclaration(node) || !node.importClause) {
      return;
    }
    const bindings = node.importClause.namedBindings;
    if (bindings && ts.isNamespaceImport(bindings)) {
      namespaces.add(bindings.name.text);
      return;
    }
    if (!bindings || !ts.isNamedImports(bindings)) {
      return;
    }
    for (const element of bindings.elements) {
      const original = (element.propertyName ?? element.name).text;
      if (reads.includes(original)) {
        locals.add(element.name.text);
      }
    }
  });

  return { locals, namespaces };
}

/** The read this call invokes, whether written bare, aliased, or namespaced. */
function calledRead(
  node: ts.CallExpression,
  bindings: ReadBindings,
): string | null {
  if (ts.isIdentifier(node.expression)) {
    return bindings.locals.has(node.expression.text)
      ? node.expression.text
      : null;
  }
  if (
    ts.isPropertyAccessExpression(node.expression) &&
    ts.isIdentifier(node.expression.expression) &&
    bindings.namespaces.has(node.expression.expression.text) &&
    reads.includes(node.expression.name.text)
  ) {
    return `${node.expression.expression.text}.${node.expression.name.text}`;
  }
  return null;
}

/**
 * Does this block re-raise? Answered by walking for a real ThrowStatement or a
 * real call to unstable_rethrow, not by matching the source text.
 *
 * Text matching accepted a `throw` written inside a string literal or a
 * comment, which is a rethrow that is not code at all. Nested functions are
 * not descended into: a throw inside a callback declared in the catch does not
 * re-raise the caught error.
 */
function reRaises(block: ts.Node): boolean {
  let found = false;
  const visit = (node: ts.Node): void => {
    if (found) {
      return;
    }
    if (
      ts.isFunctionDeclaration(node) ||
      ts.isFunctionExpression(node) ||
      ts.isArrowFunction(node)
    ) {
      return;
    }
    if (ts.isThrowStatement(node)) {
      found = true;
      return;
    }
    if (
      ts.isCallExpression(node) &&
      ts.isIdentifier(node.expression) &&
      node.expression.text === "unstable_rethrow"
    ) {
      found = true;
      return;
    }
    node.forEachChild(visit);
  };
  visit(block);
  return found;
}

/** Every control-plane read call, with whether something makes it survivable. */
function untoleratedReads(file: string): string[] {
  const source = ts.createSourceFile(
    file,
    readFileSync(file, "utf8"),
    ts.ScriptTarget.Latest,
    true,
    file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );

  const isRouteHandler = file.endsWith(`${sep}route.ts`);
  const bindings = readBindings(source);
  const offenders: string[] = [];

  const visit = (node: ts.Node): void => {
    const read = ts.isCallExpression(node) ? calledRead(node, bindings) : null;
    if (read) {
      const tolerated =
        insideCallTo(node, ["tolerate", "tolerateBoxed"]) ||
        isCaught(node, !isRouteHandler) ||
        insideTry(node, !isRouteHandler);
      if (!tolerated) {
        const { line } = source.getLineAndCharacterOfPosition(node.getStart());
        offenders.push(`${relative(WEB_CONSOLE, file)}:${line + 1}: ${read}()`);
      }
    }
    node.forEachChild(visit);
  };

  visit(source);
  return offenders;
}

const files = SCAN_ROOTS.flatMap((root) =>
  sourceFilesUnder(join(WEB_CONSOLE, root)),
).filter((file) => !EXEMPT.some((e) => file.endsWith(sep + e) || file.endsWith(e)));

describe("control-plane reads cannot throw out of a render", () => {
  it("derives the read list from the client module's exports", () => {
    // A hand-kept list drifts; this asserts the derivation found the module,
    // not a plausible empty answer.
    expect(reads.length).toBeGreaterThan(20);
    expect(reads).toContain("getViewer");
    expect(reads).toContain("getBalance");
    expect(reads).toContain("getInvoice");
    expect(reads).toContain("getInvoicePdfUrl");
  });

  it("scans route handlers and shared helpers, not only page components", () => {
    expect(files.length).toBeGreaterThan(100);
    expect(files.some((f) => f.endsWith(".ts"))).toBe(true);
    expect(files.some((f) => f.includes(join("app", "console")))).toBe(true);
    expect(
      files.some((f) => f.endsWith(join("lib", "analytics", "overview-fetch.ts"))),
    ).toBe(true);
  });

  it("leaves no control-plane read able to throw out of a render", () => {
    const offenders = files.flatMap(untoleratedReads).sort();

    expect(offenders).toEqual([]);
  });
});
