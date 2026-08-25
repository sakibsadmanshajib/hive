#!/usr/bin/env node
// Deploy diagnosability guard (issues #1084, #1103).
//
// deploy-demo-box.yml is the only thing standing between a merge to main and
// the live demo box, and DEMO.md's premise is that the two are the same event.
// Every rule below exists because that premise broke in a way the run itself
// could not explain.
//
//   1. The job's only diagnostic step sat in the middle of the step list. An
//      `if: failure()` step runs wherever it sits, but it can only report on
//      what already happened, so the six steps after it failed with no
//      `compose ps`, no container logs and no disk figures. Run 32842676794
//      (2026-08-25) failed in the smoke test with `api=0` twelve times and
//      left nothing behind to say why.
//
//   2. `docker compose up -d --build` prints the same shape of output whether
//      it recreated a service or decided it had nothing to do, and exits 0
//      either way. Without an explicit comparison of the running container's
//      image against the image the tag now points at, a deploy that reached
//      nothing is indistinguishable from one that had nothing to reach. That
//      is issue #1103 in one sentence.
//
//   3. `curl -fsS <url> && ok=1` throws away the status code, so a service
//      answering 503 "degraded" (which edge-api does whenever it cannot reach
//      control-plane) reads exactly like a dead host or a broken tunnel. The
//      smoke test has to record what it saw, and has to probe the same
//      services over loopback so a public-only failure is identifiable as an
//      ingress fault rather than a bad deploy.
//
//   4. The chat image's `npm ci` pulled a ~120 MB Cypress desktop app from
//      download.cypress.io at build time, over the box's home broadband. It
//      took the 2026-08-24 21:44 deploy down outright. Nothing in the image
//      runs Cypress.
//
// Each of these is a one-line edit away from regressing silently, which is why
// they are asserted here rather than left to a comment.

import { readFileSync } from "node:fs";
import { parse } from "yaml";

const WORKFLOW = ".github/workflows/deploy-demo-box.yml";
const DOCKERFILE = "deploy/docker/Dockerfile.open-webui";

const DUMP_STEP = "Dump container status + logs on failure";
const RECREATE_STEP = "Rebuild + recreate changed services";
const DRIFT_STEP = "Assert the running containers are on the images just built";
const SMOKE_STEP = "Smoke test public hostnames";

const errors = [];

const workflow = parse(readFileSync(WORKFLOW, "utf8"));
const steps = workflow?.jobs?.deploy?.steps;
if (!Array.isArray(steps) || steps.length === 0) {
  console.error(`${WORKFLOW} has no jobs.deploy.steps array; cannot lint it.`);
  process.exit(1);
}

const names = steps.map((s) => s?.name ?? "(unnamed)");
const indexOf = (name) => names.indexOf(name);
const stepBody = (name) => {
  const step = steps[indexOf(name)];
  return typeof step?.run === "string" ? step.run : "";
};

// 1. The dump is last, and fires on failure.
const dumpAt = indexOf(DUMP_STEP);
if (dumpAt === -1) {
  errors.push(
    `${WORKFLOW}: the deploy job has no step named "${DUMP_STEP}". A deploy that fails with no container status, no logs and no disk figures cannot be diagnosed from the run at all.`,
  );
} else {
  if (dumpAt !== steps.length - 1) {
    errors.push(
      `${WORKFLOW}: "${DUMP_STEP}" is step ${dumpAt + 1} of ${steps.length}, not the last one. It can only report on steps that already ran, so every step after it fails blind. Steps that would currently fail blind: ${names.slice(dumpAt + 1).join(", ")}.`,
    );
  }
  if (String(steps[dumpAt]?.if ?? "").replace(/\s/g, "") !== "failure()") {
    errors.push(
      `${WORKFLOW}: "${DUMP_STEP}" must carry \`if: failure()\` so it fires for a failure in any earlier step.`,
    );
  }
  const dump = stepBody(DUMP_STEP);
  for (const needle of ["docker system df", "ps -a", "logs"]) {
    if (!dump.includes(needle)) {
      errors.push(
        `${WORKFLOW}: "${DUMP_STEP}" no longer runs \`${needle}\`. Disk exhaustion, an exited container and container logs are the three things this box's deploy failures have actually needed.`,
      );
    }
  }
}

// 2. The image-drift assertion exists and runs straight after the recreate.
const recreateAt = indexOf(RECREATE_STEP);
const driftAt = indexOf(DRIFT_STEP);
if (recreateAt === -1) {
  errors.push(`${WORKFLOW}: the deploy job has no step named "${RECREATE_STEP}".`);
} else if (driftAt === -1) {
  errors.push(
    `${WORKFLOW}: the deploy job has no step named "${DRIFT_STEP}". Without it, \`up -d --build\` reporting success proves nothing about what the box is now serving (issue #1103).`,
  );
} else if (driftAt !== recreateAt + 1) {
  errors.push(
    `${WORKFLOW}: "${DRIFT_STEP}" must run immediately after "${RECREATE_STEP}"; it is currently ${driftAt - recreateAt} steps later. Anything in between can recreate a container and mask the drift this check exists to find.`,
  );
}
if (driftAt !== -1) {
  const drift = stepBody(DRIFT_STEP);
  // The comparison is the check. A version of this step that stopped
  // resolving the tag would still print reassuring lines and pass.
  if (!drift.includes("{{.Image}}") || !drift.includes("docker image inspect")) {
    errors.push(
      `${WORKFLOW}: "${DRIFT_STEP}" must compare each container's created-from image ID (\`docker inspect -f '{{.Image}}'\`) against the ID its tag resolves to now (\`docker image inspect\`). Anything weaker cannot tell a cached no-op rebuild from a deploy that never landed.`,
    );
  }
}

// 3. The smoke test records what it saw.
const smokeAt = indexOf(SMOKE_STEP);
if (smokeAt === -1) {
  errors.push(`${WORKFLOW}: the deploy job has no step named "${SMOKE_STEP}".`);
} else {
  const smoke = stepBody(SMOKE_STEP);
  if (!smoke.includes("%{http_code}")) {
    errors.push(
      `${WORKFLOW}: "${SMOKE_STEP}" must record each probe's HTTP status (\`curl -w '%{http_code}'\`). A bare \`curl -fsS\` verdict cannot tell a 503 "degraded" response from an unreachable host, and both have already happened here.`,
    );
  }
  if (!smoke.includes("localhost:8080") || !smoke.includes("localhost:8081")) {
    errors.push(
      `${WORKFLOW}: "${SMOKE_STEP}" must also probe edge-api and control-plane over loopback (localhost:8080, localhost:8081, per deploy/cloudflare/tunnel-ingress.json) when the public probe fails. Public-down plus loopback-up is a Cloudflare Tunnel ingress fault, not a bad deploy, and nothing else in the run distinguishes the two.`,
    );
  }
}

// 4. The chat image does not fetch the Cypress binary at build time.
const dockerfile = readFileSync(DOCKERFILE, "utf8");
const npmCi = dockerfile
  .split("\n")
  .find((line) => /^RUN\b.*\bnpm ci\b/.test(line));
if (!npmCi) {
  errors.push(`${DOCKERFILE}: no \`RUN ... npm ci\` line found; this lint is checking the wrong file.`);
} else if (!npmCi.includes("CYPRESS_INSTALL_BINARY=0")) {
  errors.push(
    `${DOCKERFILE}: \`${npmCi.trim()}\` must set CYPRESS_INSTALL_BINARY=0. cypress is a devDependency of the vendored tree and its postinstall downloads a ~120 MB desktop app from download.cypress.io during the image build; nothing in the image runs Cypress, and that download failed the whole deploy on 2026-08-24.`,
  );
}

if (errors.length > 0) {
  console.error("Deploy diagnosability check FAILED:");
  for (const e of errors) console.error(`  - ${e}`);
  process.exit(1);
}

console.log(
  `Deploy diagnosability OK: ${steps.length} deploy steps, dump-on-failure last, image drift asserted immediately after the recreate, smoke test records status codes plus a loopback fallback, chat image build fetches no Cypress binary.`,
);
