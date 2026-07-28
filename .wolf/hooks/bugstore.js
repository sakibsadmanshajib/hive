#!/usr/bin/env node
// Manual entry point for the OpenWolf bug memory store.
//
// .wolf/buglog.jsonl is the tracked source of truth; .wolf/buglog.json is a
// generated, gitignored aggregate that exists so `openwolf bug search` keeps
// working. The session-start hook rebuilds the aggregate automatically, so this
// script is only needed outside a Claude Code session, for example right after
// cloning the repo.
//
//   node .wolf/hooks/bugstore.js sync
//   node .wolf/hooks/bugstore.js add '{"error_message":"...","root_cause":"...","fix":"...","tags":["ci"]}'
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { appendBugs, syncBugAggregate, newBugId } from "./shared.js";

const wolfDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const [command, payload] = process.argv.slice(2);

if (command === "sync") {
    console.log(`buglog: ${syncBugAggregate(wolfDir)} entries`);
}
else if (command === "add") {
    if (!payload) {
        console.error("add requires a JSON object argument");
        process.exit(1);
    }
    let bug;
    try {
        bug = JSON.parse(payload);
    }
    catch (err) {
        console.error(`add requires valid JSON: ${err instanceof Error ? err.message : String(err)}`);
        process.exit(1);
    }
    if (bug === null || typeof bug !== "object" || Array.isArray(bug)) {
        console.error("add requires a JSON object");
        process.exit(1);
    }
    if (typeof bug.error_message !== "string" || !bug.error_message.trim()) {
        console.error("add requires a non-empty error_message");
        process.exit(1);
    }
    const now = new Date().toISOString();
    const total = appendBugs(wolfDir, [{
            id: bug.id ?? newBugId(),
            timestamp: bug.timestamp ?? now,
            related_bugs: [],
            occurrences: 1,
            last_seen: now,
            ...bug,
        }]);
    console.log(`buglog: ${total} entries`);
}
else {
    console.error("usage: bugstore.js sync | bugstore.js add '<json>'");
    process.exit(1);
}
