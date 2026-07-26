# agent-engine Apptainer image

`agent-engine.def` is the build recipe for the sandbox image that the
agent-engine service launches once per agent session. Each coding-pack and
knowledge-work-pack session runs inside a container built from this definition
(see `apps/agent-engine/internal/sandbox`). The agent-engine binary needs a
prebuilt `.sif` and reads its path from `HIVE_AGENT_SIF_PATH`.

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

## Wiring it to agent-engine

Set the absolute path to the built image and (re)start the service:

```bash
export HIVE_AGENT_SIF_PATH=/opt/hive/agent-engine.sif
```

Docker Compose already forwards `HIVE_AGENT_SIF_PATH` from your `.env` into the
agent-engine service (`deploy/docker/docker-compose.yml`). Add it to `.env`:

```dotenv
HIVE_AGENT_SIF_PATH=/opt/hive/agent-engine.sif
```

agent-engine refuses to start without it (`-sif` flag or `HIVE_AGENT_SIF_PATH`;
see `apps/agent-engine/cmd/agent-engine/main.go`).

**Note:** the steps above wire the standalone `agent-engine` compose service
(`--profile agent`), which is a build/smoke-test container only (hardcoded
placeholder tenant/user, `-dry-run`, its own placeholder `-sif` path ignoring
`HIVE_AGENT_SIF_PATH`). It does not execute real per-task sandbox launches.

## Real per-task execution (control-plane)

Real agent-task sandbox launches run inside the `control-plane` process
itself via `buildAgentEngine` (`apps/control-plane/cmd/server/main.go`), gated
on five required `HIVE_AGENT_ENGINE_*` env vars (plus one optional,
`HIVE_AGENT_ENGINE_SESSION_API_KEY`) documented in `.env.example`; six total
are forwarded below.
`docker-compose.yml` forwards these into the `control-plane` service, but
setting them alone is not sufficient: `control-plane` here runs inside a
`golang:1.24-alpine` dev container, which cannot exec the host's Apptainer
directly (Apptainer's package build is glibc-linked with no musl loader in
the container, no `/dev/fuse` device, and it would need real
`CAP_SYS_ADMIN`-class privilege granted to the service that also holds
Stripe/Supabase/payment secrets). Real sandbox launches need `control-plane`
running as a bare host process on whatever box executes agent tasks, not
this dev container. That deployment-topology decision is still open.

## Verifying a built image

```bash
apptainer inspect /opt/hive/agent-engine.sif
```

The `apps/agent-engine/internal/sandbox/apptainer_integration_test.go` live
launch test (gated behind `HIVE_APPTAINER_TEST=1`) exercises a built SIF
end to end on a host that has apptainer.
