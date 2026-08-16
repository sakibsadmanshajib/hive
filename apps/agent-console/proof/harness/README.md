# agent-console capture harness

Runs `apps/agent-console` locally against stubs and screenshots the task
console in each of its states. Developer tooling only: nothing here is
imported by the app, referenced by its build, or shipped in any image.

It exists because the same throwaway pair (a GoTrue stand-in and an edge-api
stand-in behind a Next dev server) was written and thrown away three times to
produce the visual proof this repo requires before a UI change merges. This is
that pair, kept.

## One command

```bash
node apps/agent-console/proof/harness/capture.mjs
```

That starts the stub, starts `next dev`, drives a real Chromium through every
scenario, writes PNGs to `apps/agent-console/proof/harness/captures/`, and
shuts both servers down. It needs no running Hive stack, no credentials, and
no network access to Supabase or edge-api. Every origin the console talks to
points at the stub on `127.0.0.1`.

Prerequisites, both one-off:

```bash
npm install --prefix apps/agent-console     # the app's own dependencies
npm install --prefix apps/web-console       # supplies playwright + Chromium
```

Playwright is borrowed from `apps/web-console`, the one app in this repo that
already depends on a browser, so this harness adds no dependency of its own.
From a git worktree (which has no `node_modules`), point it at a checkout that
does:

```bash
HARNESS_PLAYWRIGHT_ROOT=/path/to/hive node apps/agent-console/proof/harness/capture.mjs
```

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--scenario=<name>` | `all` | Run one scenario. Names are listed below. |
| `--out=<dir>` | `apps/agent-console/proof/harness/captures/` | Where the PNGs are written. A relative value is resolved against the current working directory, not the harness directory. |
| `--note=<text>` | none | Free text stamped into each image. |
| `--port=<n>` | `3020` | Port for `next dev`. |
| `--stub-port=<n>` | `4010` | Port for the stub. |

Every screenshot carries a footer stamp with the scenario, the shot name, the
short commit, and a `+local-edits` marker when the tree is dirty, so an image
cannot drift from the tree it proves. `--note` is for captures whose whole
point is the tree they were taken against, for example a deliberately reverted
fix.

## Scenarios

| Name | What it proves |
| --- | --- |
| `poll-recovery` | The poll loop gives up after five consecutive list failures and says so, then a successful create clears that alert and restarts the loop, and the new row progresses without a reload. |
| `unknown-status` | A status outside the documented wire set renders as a visible `Unknown` row instead of being dropped from the list. |

`poll-recovery` takes roughly two minutes. Reaching the give-up threshold needs
five real failures through the console's real backoff (3s doubling to a 30s
cap), and the harness sits through it rather than reaching into the component.

## Adding a scenario

Add an entry to `SCENARIOS` in `capture.mjs`. A scenario seeds the stub through
`control(...)`, drives the page, and calls `shoot(...)` at each state worth
keeping. Nothing else needs to change.

The stub knows nothing about any particular scenario. `POST /__control` patches
its state and `GET /__control` reads it back:

| Field | Effect |
| --- | --- |
| `tasks` | Replaces the task list. Missing fields are filled in. |
| `listFailures` | Answers that many consecutive `GET /v1/agent/tasks` with a 503. |
| `createStatus` | Status a newly created task starts in. |
| `advanceOnList` | `{ id: ["running", "succeeded"] }`, one step per list request. Models the server moving a task while the console polls. |
| `coworkEnabled` | Value reported for the `ENABLE_COWORK` feature gate. |

`status` is passed through without validation, which is how the `unknown` row
is exercised.

## No credentials

The stub mints an unsigned, obviously fake JWT for a fixed
`@example.invalid` identity and checks nothing. The anon key is the literal
string `stub-anon-key-not-a-credential`. No value in this directory is
accepted by any real system, and no captured URL carries a token in its query
string. Keep it that way: `npm run lint:proof-tokens` guards the text half of
that rule for `docs/proof/`, and nothing can inspect screenshot pixels for you.

## The live sibling: `capture-live.mjs`

`capture.mjs` proves rendering. It cannot prove anything that only exists when
a sandbox really starts, because it starts none. `capture-live.mjs` is the
other half: it drives the same console against a booted edge-api,
control-plane and agent-engine launch daemon, so a task it creates runs inside
a real Apptainer sandbox.

It is not run by hand on the dev box. The sandbox image is `linux/amd64` and
unbuildable under WSL2, so the stack that can run it is stood up by
`.github/workflows/agent-visual-proof.yml`:

```bash
gh workflow run "agent visual proof" -f pr=<number>
```

That checks out `refs/pull/<number>/merge`, boots the stack with the launcher
on the socket arm, runs the scenarios below, pushes the captures to a side
branch and posts them as a comment on that pull request.

| Scenario | What it proves |
| --- | --- |
| `launch-liveness` | A task created in the browser reaches Running because a real sandbox came up for it. Asserts nothing version specific, so it doubles as the workflow's own self-test. |
| `create-returns-queued` | Issue #881: create answers 201 with a `queued` task inside edge-api's 15 second client bound, instead of blocking on a cold sandbox and 500ing. |
| `cancel-frees-slot` | Issue #886: with `HIVE_QUOTA_USER_CONCURRENCY=1`, a second create is refused while the slot is held, the cancel takes the launcher's live session count from 1 to 0, and the next create launches. |

The slot count is read from the launcher's `sessions/` directory rather than
from a status badge: `SandboxEngine.Launch` creates exactly one directory
there per live session and `reap()` removes it in the same call that releases
the quota slot, so the number is the slot, not a proxy for it.
