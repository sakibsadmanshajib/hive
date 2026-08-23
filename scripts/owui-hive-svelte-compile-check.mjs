// Compiles every Hive authored .svelte component in the vendored chat front
// end and fails on the first compiler error.
//
// Why this exists: the only pre-merge check on vendor/open-webui/src/lib/hive
// was a vitest run over the .ts files there, and vitest never touches a
// .svelte file that no test imports. A component whose markup does not compile
// therefore merged green and only failed hours later inside the image build in
// deploy-demo-box, which is the one build that gates the demo box. That is
// exactly what happened to AgentSchedules.svelte on 2026-08-23: a
// `<svelte:window>` nested inside an `{#if}` block, rejected by the Svelte
// compiler, red deploy, no CI signal.
//
// Compilation only. No bundling and no import resolution, so the components
// can be compiled out of the vendored tree without installing the chat front
// end's whole dependency graph, which is the same trick
// scripts/test-owui-hive-frontend.sh already plays for the unit tests.
import { readdirSync, readFileSync } from "node:fs";
import { compile } from "svelte/compiler";

const dir = process.argv[2] ?? ".";
const files = readdirSync(dir)
  .filter((f) => f.endsWith(".svelte"))
  .sort();

if (files.length === 0) {
  console.error(`no .svelte files found in ${dir}`);
  process.exit(1);
}

let failed = 0;
for (const file of files) {
  const source = readFileSync(`${dir}/${file}`, "utf8");
  try {
    compile(source, { filename: file, generate: "client" });
    console.log(`ok   ${file}`);
  } catch (err) {
    failed += 1;
    const at = err.start ? ` (${err.start.line}:${err.start.column})` : "";
    console.error(`FAIL ${file}${at}: ${err.message}`);
  }
}

console.log(`${files.length - failed}/${files.length} components compiled`);
process.exit(failed === 0 ? 0 : 1);
