# agent-engine Apptainer image

`agent-engine.def` is the build recipe for the sandbox image that the
agent-engine service launches once per agent session. Each coding-pack and
knowledge-work-pack session runs inside a container built from this definition
(see `apps/agent-engine/internal/sandbox`). Real per-task launches read the
image path from `HIVE_AGENT_ENGINE_SIF_PATH`; the similarly named
`HIVE_AGENT_SIF_PATH` only affects the smoke-test container described at the
end of this file (issue #781).

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

## Wiring it up (real per-task execution)

This is the part that makes Cowork tasks actually run. Do this, not the
`HIVE_AGENT_SIF_PATH` block further down, which only affects a smoke-test
container.

The launcher runs on the host, as an ordinary unprivileged user, and
`control-plane` talks to it over a Unix socket (issue #780). `control-plane`
cannot exec Apptainer itself: it runs in an Alpine container with no glibc
loader for that binary, no `/dev/fuse`, and no `CAP_SYS_ADMIN`-class
privilege, and granting it those privileges was refused deliberately, since it
is the same process that holds the Stripe keys, the Supabase service-role key
and the platform database DSN.

```bash
# On the host, once per deploy. Idempotent; it fetches the CI-built .sif when
# the host has none, builds the launcher, and restarts its systemd user unit.
HIVE_AGENT_ENGINE_LLM_MODEL=openai/hive-default \
HIVE_AGENT_ENGINE_LLM_BASE_URL=https://api-hive.scubed.co/v1 \
HIVE_AGENT_ENGINE_LLM_API_KEY=<gateway key> \
CONTROL_PLANE_INTERNAL_TOKEN=<internal token> \
  bash scripts/install-agent-engine-host.sh
```

Then point `control-plane` at the socket, in `.env`:

```dotenv
HIVE_AGENT_ENGINE_SOCKET_DIR=/home/<user>/agent-runtime/run
HIVE_AGENT_ENGINE_SOCKET=/run/hive-agent/engine.sock
```

`deploy-demo-box.yml` does both of these on every deploy already.

Host requirements, all verified on the demo box on 2026-08-11:

* Apptainer installed, `/dev/fuse` present, unprivileged user namespaces on.
* A systemd **user** session. Apptainer enforces the per-session memory, CPU
  and PID limits through rootless cgroups, and without `XDG_RUNTIME_DIR` and
  `DBUS_SESSION_BUS_ADDRESS` every launch fails with
  `cannot use cgroups - DBUS_SESSION_BUS_ADDRESS is not set`. Running the
  launcher as a systemd user unit supplies both.
* A model endpoint the sandbox may reach. Its host is added to the sandbox
  egress allowlist automatically, because a tenant with no egress policy row
  resolves to deny-all and the model call goes through the same proxy.

The full variable set each side reads is in `.env.example`. Only the socket
variable matters to `control-plane`; everything else is read by the launcher.

## Supervision and the health signal (issue #1510)

The launcher is the only arm that runs a real task on any deployment this repo
ships, so it is supervised rather than left as a bare process. The installer
above renders both halves out of `deploy/systemd-user/` and installs them into
`~/.config/systemd/user/`.

* `hive-agent-engine.service` runs the launcher with `Restart=always` and
  `StartLimitIntervalSec=0`, enabled into `default.target`. It used to be a
  transient unit created by `systemd-run --user --collect`, which meant a
  reboot erased the definition (transient units live in tmpfs), a stop
  garbage-collected the unit rather than leaving a failed one to notice, a
  clean exit or a `SIGTERM` was never restarted at all, and five crashes in ten
  seconds exhausted the default start limit for good. Lingering is required and
  already on for this user (`loginctl show-user <user> -p Linger`); without it
  no user unit starts at boot.
* `hive-agent-engine-health.timer` runs `agent-engine-health-probe.sh` every
  five minutes from an installed copy under the runtime directory, never from
  the repo checkout. It posts `HiveAgentEngineDown` to Alertmanager on
  `localhost:9093` when the unit is not active, the socket is missing, or
  `/health` does not answer.
* A **cron** entry runs the same script with `--check` every fifteen minutes,
  and the different scheduler is the point. `--check` reads the last-success
  stamp and nothing else, posting `HiveAgentEngineProbeStale` when no probe has
  succeeded in fifteen minutes. A staleness check living only inside the timed
  probe cannot fire when the timer stops, because the probe never runs: it
  posts nothing, the firing alert lapses through Alertmanager's
  `resolve_timeout`, and the silence reads as health. `--check` never writes the
  stamp, since a second writer would keep it fresh forever and hide exactly the
  condition it looks for. Same split `scripts/backup-box.sh` already uses on
  this box for the same reason. Both alerts route through the existing tree to
  the hive-ops email receiver.

What that pair still does not cover, stated rather than implied: if cron and
the systemd user manager are both dead, the box is dead, and that is
`external-uptime-probe.yml`'s job from outside this box's own network path.

`/health` answers for the ability to LAUNCH, not for the mux being alive. It
checks that `apptainer` is on `PATH`, that the SIF exists and is non-empty,
readable and carries the SIF magic, that the packs directory is present, and
that the workspace and run directories are writable, returning 503 with the
named failures when any of that is false. Two different outages, kept separate:
a dead launcher PROCESS takes the socket with it and the probe sees that before
it issues a request, while a deleted or half-downloaded IMAGE leaves a perfectly
healthy process that cannot run a single task. The installer deliberately keeps
the SIF out of its restart fingerprint, so nothing restarts on that and, until
this check existed, nothing observed it either.

Restarting the launcher kills the `apptainer` child holding every in-flight
Cowork session, so the installer restarts it only when the built binary, the
rendered env file, the entry script or the unit file actually changed. Writing
the unit, reloading the manager and enabling it happen on every run, because
none of those three touch a running process.

At-a-glance checks on the box:

```bash
systemctl --user status hive-agent-engine.service
systemctl --user is-enabled hive-agent-engine.service     # expect: enabled
systemctl --user list-timers hive-agent-engine-health.timer
crontab -l | grep agent-engine-health-probe               # the staleness lane
journalctl --user -u hive-agent-engine -n 100
journalctl --user -u hive-agent-engine-health -n 50
curl -sS --fail-with-body --unix-socket \
  /home/sakib/agent-runtime/run/engine.sock http://localhost/health
```

A rollback is safe. `scripts/normalize-agent-engine-unit.sh` runs before the
installer on every deploy and hands the unit name back when the checkout being
deployed predates the unit file, because `systemd-run` refuses a name that a
fragment file already holds and `systemctl stop` does not unload a fragment.
Without that guard, a checkout from before this change cannot deploy onto a box
that has the unit, and the failing deploy stops the launcher on its way past.
A full revert of this change reverts that guard too; the manual clear is in the
script's own header.

If the unit fails to start during a deploy, the installer's own `/health` gate
fails that step and the deploy run goes red. If it dies later, systemd brings
it back within five seconds. If it cannot be brought back, it restart-loops,
the socket never answers, and the probe mails within five minutes.

## The standalone smoke-test container

`HIVE_AGENT_SIF_PATH` (note: a different variable from
`HIVE_AGENT_ENGINE_SIF_PATH` above) wires the `agent-engine` compose service
under `--profile agent`. That service is a build and smoke-test container
only: hardcoded placeholder tenant and user, `-dry-run`, and its own
placeholder `-sif` path. It never executes a real per-task launch, so setting
this variable changes nothing about whether Cowork works (issue #781).

```dotenv
HIVE_AGENT_SIF_PATH=/opt/hive/agent-engine.sif
```

## Verifying a built image

```bash
apptainer inspect /opt/hive/agent-engine.sif
```

The `apps/agent-engine/internal/sandbox/apptainer_integration_test.go` live
launch test (gated behind `HIVE_APPTAINER_TEST=1`) exercises a built SIF
end to end on a host that has apptainer.
