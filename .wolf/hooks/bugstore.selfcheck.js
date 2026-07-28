#!/usr/bin/env node
// Self-check for the OpenWolf bug memory store. No test framework: run it with
//
//   node .wolf/hooks/bugstore.selfcheck.js
//
// It exercises the store against a throwaway directory and asserts the three
// properties the JSONL format exists to guarantee: appends never rewrite an
// existing line, the generated aggregate stays a faithful projection, and a
// concurrent append merged in by git is picked up without a conflict.
import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { readBugs, readAllBugs, appendBugs, appendVolatileBug, syncBugAggregate, newBugId, bugLogPaths } from "./shared.js";

const wolfDir = fs.mkdtempSync(path.join(os.tmpdir(), "wolf-bugstore-"));
const { jsonl, aggregate } = bugLogPaths(wolfDir);

// Empty store reads as empty rather than throwing.
assert.deepEqual(readBugs(wolfDir), []);

// Append, then confirm the aggregate matches the JSONL exactly.
appendBugs(wolfDir, [{ id: "bug-a", error_message: "first", tags: ["x"] }]);
appendBugs(wolfDir, [{ id: "bug-b", error_message: "second", tags: ["y"] }]);
assert.equal(readBugs(wolfDir).length, 2);
assert.deepEqual(JSON.parse(fs.readFileSync(aggregate, "utf-8")), {
    version: 1,
    bugs: readBugs(wolfDir),
});

// The first line must be byte-identical after the second append: the store is
// append only, which is what makes `merge=union` safe.
const lines = fs.readFileSync(jsonl, "utf-8").split("\n").filter((l) => l.trim());
assert.equal(lines[0], JSON.stringify({ id: "bug-a", error_message: "first", tags: ["x"] }));
assert.equal(lines.length, 2);

// Simulate what git leaves behind after a union merge of two branches that each
// appended: an extra line the aggregate has not seen yet.
fs.appendFileSync(jsonl, JSON.stringify({ id: "bug-c", error_message: "merged in" }) + "\n");
assert.equal(syncBugAggregate(wolfDir), 3);
assert.deepEqual(
    JSON.parse(fs.readFileSync(aggregate, "utf-8")).bugs.map((b) => b.id),
    ["bug-a", "bug-b", "bug-c"],
);

// A bug written straight into the aggregate by `openwolf bug add` is absorbed
// into the JSONL instead of being dropped on the next rebuild.
const strayAggregate = JSON.parse(fs.readFileSync(aggregate, "utf-8"));
strayAggregate.bugs.push({ id: "bug-cli", error_message: "added by the CLI" });
fs.writeFileSync(aggregate, JSON.stringify(strayAggregate, null, 2));
assert.equal(syncBugAggregate(wolfDir), 4);
assert.ok(readBugs(wolfDir).some((b) => b.id === "bug-cli"));
// Idempotent: syncing again must not duplicate it.
assert.equal(syncBugAggregate(wolfDir), 4);

// A hook guess is searchable through the aggregate but must never reach the
// tracked JSONL, not on write and not through a later rebuild.
const jsonlBefore = fs.readFileSync(jsonl, "utf-8");
appendVolatileBug(wolfDir, { id: "bug-guess", error_message: "inline fix", tags: ["auto-detected", "value"] });
assert.equal(fs.readFileSync(jsonl, "utf-8"), jsonlBefore);
assert.ok(readAllBugs(wolfDir).some((b) => b.id === "bug-guess"));
assert.ok(!readBugs(wolfDir).some((b) => b.id === "bug-guess"));
assert.equal(syncBugAggregate(wolfDir), 4);
assert.equal(fs.readFileSync(jsonl, "utf-8"), jsonlBefore);
assert.ok(!readAllBugs(wolfDir).some((b) => b.id === "bug-guess"));

// A volatile guess must survive a later durable append. Only syncBugAggregate
// clears it, which the block above already asserts.
appendVolatileBug(wolfDir, { id: "bug-guess-2", error_message: "added error handling", tags: ["auto-detected", "error-handling"] });
appendBugs(wolfDir, [{ id: "bug-d", error_message: "durable append after a guess" }]);
assert.ok(readAllBugs(wolfDir).some((b) => b.id === "bug-guess-2"), "durable append dropped a volatile guess");
assert.ok(readAllBugs(wolfDir).some((b) => b.id === "bug-d"));
assert.ok(!readBugs(wolfDir).some((b) => b.id === "bug-guess-2"), "a guess leaked into the tracked JSONL");
assert.equal(readBugs(wolfDir).length, 5);

// Ids must not collide the way the old count-derived scheme did.
const ids = new Set(Array.from({ length: 500 }, () => newBugId()));
assert.equal(ids.size, 500);

fs.rmSync(wolfDir, { recursive: true, force: true });
console.log("bugstore selfcheck OK");
