# agent-engine Apptainer image

`agent-engine.def` is the build recipe for the sandbox image that the
agent-engine service launches once per agent session. Each coding-pack and
knowledge-work-pack session runs inside a container built from this definition
(see `apps/agent-engine/internal/sandbox`). Every launch path needs a prebuilt
`.sif`; which variable points at it depends on the path, and getting that
wrong is issue #781, so read "Wiring it up" below before setting anything.

The image targets `linux/amd64` only. It cannot be built on this repo's WSL2
dev box (no rootless user-namespace Apptainer support there), so the image is
built in CI or on a real demo/production host.

## Getting the .sif

### Option A: download the CI-built image (no apptainer needed locally)

The `agent-engine SIF` workflow (`.github/workflows/agent-engine-sif.yml`)
builds the image on every change to `deploy/apptainer/**`,
`vendor/openhands/**`, or `apps/agent-engine/packs/**`, and on
`workflow_dispatch`. It uploads the result as the `agent-engine-sif` artifact.

```bash
# Trigger a build on demand (or use the latest successful run):
gh workflow run "agent-engine SIF"

# Download the artifact from the most recent successful run:
gh run download -n agent-engine-sif -D /opt/hive
# -> /opt/hive/agent-engine.sif
```

### Option B: build it on the host (host has apptainer installed)

```bash
make agent-sif                 # from the repo root -> deploy/apptainer/agent-engine.sif
# or, choosing an output path:
deploy/apptainer/build.sh /opt/hive/agent-engine.sif
```

`build.sh` handles the working-directory detail so the def's `../../` file
sources resolve correctly. Building a docker-bootstrap image needs root or
fakeroot: run the script under `sudo`, or pass
`APPTAINER_BUILD_ARGS=--fakeroot` for a rootless host.

## Wiring it up

There are two launch paths and they read different variables. Almost everyone
arriving here wants the first one.

### Real per-task execution (Cowork, agent tasks) — `HIVE_AGENT_ENGINE_*`

Real agent-task sandbox launches run inside the `control-plane` process
itself via `buildAgentEngine` (`apps/control-plane/cmd/server/main.go`), gated
on five required env vars (plus one optional,
`HIVE_AGENT_ENGINE_SESSION_API_KEY`), all documented in `.env.example` and all
forwarded into the `control-plane` service by `docker-compose.yml`:

```dotenv
HIVE_AGENT_ENGINE_SIF_PATH=/opt/hive/agent-engine.sif
HIVE_AGENT_ENGINE_PACKS_DIR=/opt/hive/packs
HIVE_AGENT_ENGINE_WORKSPACE_ROOT=/opt/hive/workspaces
HIVE_AGENT_ENGINE_RUN_DIR=/opt/hive/run
HIVE_AGENT_ENGINE_PROFILE_ID=<uuid of a public.agent_profiles row>
```

`HIVE_AGENT_SIF_PATH` is **not** one of them and does nothing here. Setting it
and expecting agent tasks to run is issue #781.

If any of the five is empty, control-plane falls back to
`agenttask.NotConfiguredEngine`: every submitted task is persisted and
immediately failed with `agent engine is not available on this deployment`,
which the agent console shows as Blocked with "The agent runtime is not
configured on this deployment". control-plane also logs a WARN at boot naming
each missing variable, so this tells you which one is missing:

```bash
docker compose logs control-plane | grep "agent engine not configured"
```

Setting all five is necessary but **not sufficient** on the docker compose
topology: `control-plane` there runs inside a `golang:1.24-alpine` dev
container, which cannot exec the host's Apptainer directly (Apptainer's
package build is glibc-linked with no musl loader in the container, no
`/dev/fuse` device, and it would need real `CAP_SYS_ADMIN`-class privilege
granted to the service that also holds Stripe/Supabase/payment secrets). On
that topology, setting the five moves the failure from `agent engine is not
available on this deployment` to `agent engine could not start the task`; it
does not make a task run. Real sandbox launches need `buildAgentEngine`
running on a substrate that can exec Apptainer, which the compose container
is not. That deployment-topology decision is still open and tracked in issue
#780.

### Standalone `agent-engine` CLI — `HIVE_AGENT_SIF_PATH`

`HIVE_AGENT_SIF_PATH` is only the default for the `agent-engine` binary's own
`-sif` flag (`apps/agent-engine/cmd/agent-engine/main.go`), which refuses to
start without one or the other. It matters when you run that binary by hand:

```bash
export HIVE_AGENT_SIF_PATH=/opt/hive/agent-engine.sif
```

It is not forwarded into any compose service. The `agent-engine` compose
service (`--profile agent`) is a build/smoke-test container only (hardcoded
placeholder tenant/user, `-dry-run`, and its own placeholder `-sif` on the
command line, which overrides this default). It does not execute real per-task
sandbox launches.

## Verifying a built image

```bash
apptainer inspect /opt/hive/agent-engine.sif
```

The `apps/agent-engine/internal/sandbox/apptainer_integration_test.go` live
launch test (gated behind `HIVE_APPTAINER_TEST=1`) exercises a built SIF
end to end on a host that has apptainer.
