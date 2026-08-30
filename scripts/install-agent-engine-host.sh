#!/usr/bin/env bash
# Install and (re)start the agent-engine launch daemon on a host that has
# Apptainer, for issue #780.
#
# Why this exists at all: apps/agent-engine execs the host's `apptainer`
# binary, and control-plane runs in an Alpine container that cannot do that
# (no glibc loader, no /dev/fuse, no CAP_SYS_ADMIN). Giving that container
# those privileges was refused on purpose: it holds the Stripe keys, the
# Supabase service-role key and the platform database DSN, so a sandbox
# escape there is the worst case on the box. Instead the launcher runs here,
# as an ordinary unprivileged user, and control-plane reaches it over a Unix
# socket bind-mounted into the container. Nothing in this script needs root.
#
# Idempotent, and since issue #921 that is load bearing rather than merely
# tidy: a run whose built binary, rendered env file, entry script and unit
# file all match what the healthy running daemon was started with leaves that
# process alone instead of restarting it, because a restart kills every
# in-flight Cowork session. See "Restart only when something changed" below.
# Prints no secret values.
#
# Since issue #1510 this also installs the supervision the launcher used to
# lack. It was started by `systemd-run --user --collect`, which produces a
# TRANSIENT unit under /run/user/<uid>/systemd/transient. That tree is tmpfs,
# so a reboot erased the unit definition outright and nothing started the
# launcher again; CollectMode=inactive-or-failed then garbage-collected the
# unit whenever it stopped, so a dead launcher did not even leave a failed
# unit behind to notice. Now the unit is a real enabled user unit file
# rendered from deploy/systemd-user/hive-agent-engine.service, and a five
# minute health timer posts to Alertmanager when the launcher stops serving,
# so its absence is loud instead of surfacing the first time someone runs a
# task.
#
# Required environment:
#   HIVE_AGENT_ENGINE_LLM_MODEL     model alias the sandboxed agent calls
#   HIVE_AGENT_ENGINE_LLM_BASE_URL  OpenAI-compatible base URL for that model
#   HIVE_AGENT_ENGINE_LLM_API_KEY   key for that endpoint
#   CONTROL_PLANE_INTERNAL_TOKEN    shared internal-service token
# Optional:
#   REPO_DIR      repo checkout (default /home/sakib/hive)
#   RUNTIME_DIR   state dir     (default /home/sakib/agent-runtime)
#   GH_TOKEN      when set, a missing SIF is downloaded from the newest
#                 successful agent-engine-sif workflow artifact
#   GITHUB_REPOSITORY  owner/name for that download (default sakibsadmanshajib/hive)
set -euo pipefail

REPO_DIR="${REPO_DIR:-/home/sakib/hive}"
RUNTIME_DIR="${RUNTIME_DIR:-/home/sakib/agent-runtime}"
GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-sakibsadmanshajib/hive}"
SIF_PATH="$RUNTIME_DIR/agent-engine.sif"
BIN_PATH="$RUNTIME_DIR/bin/agent-engine"
ENV_FILE="$RUNTIME_DIR/engine.env"
RUN_ENTRY="$RUNTIME_DIR/bin/run-engine.sh"
SOCKET_DIR="$RUNTIME_DIR/run"
SOCKET_PATH="$SOCKET_DIR/engine.sock"
UNIT_NAME="hive-agent-engine"
HEALTH_NAME="hive-agent-engine-health"
# Systemd USER units, not system ones: nothing here needs root, and the whole
# reason the launcher exists as a separate process is that it runs
# unprivileged. Linger is already enabled for this user on the demo box, which
# is what makes an enabled user unit start at boot with no interactive login.
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
UNIT_PATH="$UNIT_DIR/$UNIT_NAME.service"
HEALTH_SERVICE_PATH="$UNIT_DIR/$HEALTH_NAME.service"
HEALTH_TIMER_PATH="$UNIT_DIR/$HEALTH_NAME.timer"
# The probe runs from an installed copy under $RUNTIME_DIR, never from the repo
# checkout, for the same reason hive-box-backup.service runs an installed copy:
# a deploy that resets /home/sakib/hive must not be able to take the watchdog
# down with it.
PROBE_PATH="$RUNTIME_DIR/bin/agent-engine-health-probe.sh"
TEMPLATE_DIR="$REPO_DIR/deploy/systemd-user"
# Everything below is built and rendered to a `.next` staging path first, so the
# installed artifacts can be compared against what the running daemon was
# actually started with before deciding whether to restart it at all. See
# "Restart only when something changed" further down for why that matters.
STAGE_BIN="$BIN_PATH.next"
STAGE_ENV="$ENV_FILE.next"
STAGE_ENTRY="$RUN_ENTRY.next"
STAGE_UNIT="$UNIT_PATH.next"
FINGERPRINT_PATH="$RUNTIME_DIR/engine.fingerprint"

for var in HIVE_AGENT_ENGINE_LLM_MODEL HIVE_AGENT_ENGINE_LLM_BASE_URL \
           HIVE_AGENT_ENGINE_LLM_API_KEY CONTROL_PLANE_INTERNAL_TOKEN; do
  if [ -z "${!var:-}" ]; then
    echo "::error::$var is required (value never printed)"
    exit 1
  fi
done

command -v apptainer >/dev/null || { echo "::error::apptainer is not installed on this host"; exit 1; }

mkdir -p "$RUNTIME_DIR/bin" "$SOCKET_DIR" "$RUNTIME_DIR/workspaces" "$RUNTIME_DIR/sessions" "$UNIT_DIR"
# The socket directory is bind-mounted into control-plane, so keep it as
# narrow as the socket itself.
chmod 0700 "$SOCKET_DIR"

# ---------------------------------------------------------------------------
# The SIF. linux/amd64 only and unbuildable on the dev box (WSL2 has no
# rootless user-namespace Apptainer), so CI builds it and this pulls the
# newest successful build when the host has none.
# ---------------------------------------------------------------------------
if [ ! -s "$SIF_PATH" ]; then
  if [ -z "${GH_TOKEN:-}" ]; then
    echo "::error::no SIF at $SIF_PATH and GH_TOKEN is unset, cannot fetch one"
    exit 1
  fi
  echo "no SIF at $SIF_PATH, fetching the newest agent-engine-sif artifact"
  artifact_id=$(curl -sSL -H "Authorization: Bearer $GH_TOKEN" \
    "https://api.github.com/repos/$GITHUB_REPOSITORY/actions/artifacts?name=agent-engine-sif&per_page=1" \
    | python3 -c "import json,sys;a=json.load(sys.stdin)['artifacts'];print(a[0]['id'] if a and not a[0]['expired'] else '')")
  if [ -z "$artifact_id" ]; then
    echo "::error::no unexpired agent-engine-sif artifact found; run the 'agent-engine SIF' workflow"
    exit 1
  fi
  curl -sSL -H "Authorization: Bearer $GH_TOKEN" -o "$RUNTIME_DIR/sif.zip" \
    "https://api.github.com/repos/$GITHUB_REPOSITORY/actions/artifacts/$artifact_id/zip"
  python3 -c "import zipfile,sys;zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])" "$RUNTIME_DIR/sif.zip" "$RUNTIME_DIR"
  rm -f "$RUNTIME_DIR/sif.zip"
  [ -s "$SIF_PATH" ] || mv "$RUNTIME_DIR"/*.sif "$SIF_PATH"
fi
echo "SIF: $SIF_PATH ($(stat -c%s "$SIF_PATH") bytes)"

# ---------------------------------------------------------------------------
# The binary. Built in the same Go image the rest of the stack uses, statically
# (CGO_ENABLED=0), so it runs on this host with no toolchain installed.
# ---------------------------------------------------------------------------
docker run --rm \
  -v "$REPO_DIR":/workspace \
  -v "$RUNTIME_DIR/bin":/out \
  -v hive_gomodcache:/go/pkg/mod \
  -w /workspace \
  -e CGO_ENABLED=0 \
  golang:1.26-alpine \
  go build -buildvcs=false -o /out/agent-engine.next ./apps/agent-engine/cmd/agent-engine
# `go build` already emits 0755, so this is belt and braces for an odd umask.
# It is allowed to fail: under rootful Docker (every GitHub-hosted runner) the
# build ran as root and the binary is not ours to chmod, which used to abort
# the install with "Operation not permitted" even though the file was already
# correct. What has to hold is that it ends up executable, so assert that
# rather than trusting either the chmod or the builder.
chmod 0755 "$STAGE_BIN" 2>/dev/null || true
if [ ! -x "$STAGE_BIN" ]; then
  echo "::error::$STAGE_BIN is not executable after the build"
  exit 1
fi

# ---------------------------------------------------------------------------
# Configuration. Written 0600 and read only by the unit below, so the API key
# never reaches a command line, a unit property, or a log line.
# ---------------------------------------------------------------------------
#
# The unit entry point below sources this file, so every value is written
# through printf %q, Bash's own shell-safe quoting. A key or URL that happens
# to contain whitespace, a quote, a newline or $(...) is then read back as
# data rather than executed as the deploying user.
umask 077
{
  printf '%s=%q\n' HIVE_AGENT_ENGINE_SIF_PATH "$SIF_PATH"
  printf '%s=%q\n' HIVE_AGENT_ENGINE_PACKS_DIR "$REPO_DIR/apps/agent-engine/packs"
  printf '%s=%q\n' HIVE_AGENT_ENGINE_WORKSPACE_ROOT "$RUNTIME_DIR/workspaces"
  printf '%s=%q\n' HIVE_AGENT_ENGINE_RUN_DIR "$RUNTIME_DIR/sessions"
  printf '%s=%q\n' HIVE_AGENT_ENGINE_LLM_MODEL "$HIVE_AGENT_ENGINE_LLM_MODEL"
  printf '%s=%q\n' HIVE_AGENT_ENGINE_LLM_BASE_URL "$HIVE_AGENT_ENGINE_LLM_BASE_URL"
  printf '%s=%q\n' HIVE_AGENT_ENGINE_LLM_API_KEY "$HIVE_AGENT_ENGINE_LLM_API_KEY"
  printf '%s=%q\n' HIVE_AGENT_ENGINE_SESSION_API_KEY "${HIVE_AGENT_ENGINE_SESSION_API_KEY:-}"
  # Appended to the sandboxed agent's system prompt
  # (agent_context.system_message_suffix on the launch payload). Unset is the
  # normal state and the pre-existing behaviour: no agent_context is sent at
  # all and the vendored OpenHands preset produces the prompt unchanged. Set
  # it to shape how that agent behaves without touching the vendored SDK. The
  # value is written through printf %q like every other line here, so a
  # multi-line prompt containing quotes or $(...) is read back as data.
  printf '%s=%q\n' HIVE_AGENT_ENGINE_SYSTEM_MESSAGE_SUFFIX "${HIVE_AGENT_ENGINE_SYSTEM_MESSAGE_SUFFIX:-}"
  printf '%s=%q\n' CONTROL_PLANE_URL "${CONTROL_PLANE_URL:-http://127.0.0.1:8081}"
  printf '%s=%q\n' CONTROL_PLANE_INTERNAL_TOKEN "$CONTROL_PLANE_INTERNAL_TOKEN"
  # Same reasoning as CONTROL_PLANE_URL above: this daemon runs as a bare
  # host process, not inside the compose network, so edge-api's compose DNS
  # name (agent-engine's own binary reads no default at all now — see
  # serve.go) does not resolve here. This is the kill switch for
  # knowledge-work-pack artifact publishing (issue #312/#300 wiring), which
  # writes to external storage on every completed task, so it uses ${VAR-x},
  # not ${VAR:-x}: a caller who explicitly sets EDGE_API_URL="" gets that
  # empty value written through and publishing stays off, where ${VAR:-x}
  # would silently treat empty the same as unset and turn it back on. Only a
  # genuinely unset variable falls back to this host-reachable default.
  printf '%s=%q\n' EDGE_API_URL "${EDGE_API_URL-http://127.0.0.1:8080}"
  printf '%s=%q\n' HIVE_QUOTA_TENANT_CONCURRENCY "${HIVE_QUOTA_TENANT_CONCURRENCY:-4}"
  printf '%s=%q\n' HIVE_QUOTA_USER_CONCURRENCY "${HIVE_QUOTA_USER_CONCURRENCY:-2}"
  printf '%s=%q\n' HIVE_SANDBOX_MEMORY_LIMIT "${HIVE_SANDBOX_MEMORY_LIMIT:-4G}"
  printf '%s=%q\n' HIVE_SANDBOX_CPU_LIMIT "${HIVE_SANDBOX_CPU_LIMIT:-2}"
  printf '%s=%q\n' HIVE_SANDBOX_PIDS_LIMIT "${HIVE_SANDBOX_PIDS_LIMIT:-512}"
} > "$STAGE_ENV"
chmod 0600 "$STAGE_ENV"

# Apptainer enforces this sandbox's --memory/--cpus/--pids-limit through
# rootless cgroups, which need a systemd user session to delegate them. A
# plain `nohup` from a CI step has neither variable set and the launch dies
# with "cannot use cgroups - DBUS_SESSION_BUS_ADDRESS is not set", measured on
# this box. Running under the user manager (below) supplies both; they are
# pinned here as well so a hand-run daemon behaves identically.
cat > "$STAGE_ENTRY" <<EOF
#!/usr/bin/env bash
set -euo pipefail
export XDG_RUNTIME_DIR="\${XDG_RUNTIME_DIR:-/run/user/\$(id -u)}"
export DBUS_SESSION_BUS_ADDRESS="\${DBUS_SESSION_BUS_ADDRESS:-unix:path=\$XDG_RUNTIME_DIR/bus}"
set -a
. "$ENV_FILE"
set +a
exec "$BIN_PATH" -serve "$SOCKET_PATH"
EOF
chmod 0700 "$STAGE_ENTRY"
umask 022

# ---------------------------------------------------------------------------
# The unit files (issue #1510), rendered from deploy/systemd-user/.
#
# They live in the repository rather than in a heredoc here so the supervision
# policy is reviewable in a diff next to hive-box-backup.service, which is the
# working user unit this box already runs. Only paths are substituted, because
# only paths vary with RUNTIME_DIR; no unit carries a secret, and the launcher
# reads its credentials by sourcing $ENV_FILE from inside $RUN_ENTRY, so no key
# ever reaches a unit property where `systemctl show` would print it.
#
# Substitution is bash parameter replacement, not sed: every value being
# substituted is an absolute path, and sed would need its delimiter escaped
# around every one of them.
# ---------------------------------------------------------------------------
render_unit() {
  local template="$1" dest="$2" content
  [ -f "$template" ] || { echo "::error::missing unit template $template"; exit 1; }
  content=$(cat "$template")
  content=${content//@RUN_ENTRY@/$RUN_ENTRY}
  content=${content//@PROBE@/$PROBE_PATH}
  content=${content//@RUNTIME_DIR@/$RUNTIME_DIR}
  content=${content//@UNIT_NAME@/$UNIT_NAME}
  # A placeholder that survived rendering would install a unit whose ExecStart
  # is the literal string "@RUN_ENTRY@", which systemd accepts at write time
  # and fails only at start. Catch it here instead.
  case "$content" in
    *@RUN_ENTRY@*|*@PROBE@*|*@RUNTIME_DIR@*|*@UNIT_NAME@*)
      echo "::error::unsubstituted placeholder left in $dest"; exit 1 ;;
  esac
  printf '%s\n' "$content" > "$dest"
}

render_unit "$TEMPLATE_DIR/$UNIT_NAME.service" "$STAGE_UNIT"

# ---------------------------------------------------------------------------
# Restart only when something changed (issue #921).
#
# This script runs on EVERY merge to main: deploy-demo-box.yml's paths filter
# fires on deploy/**, apps/web-console/**, vendor/open-webui/**,
# supabase/migrations/** and more, none of which can change anything below.
# Stopping the unit is not free. It is a systemd user service with the default
# KillMode=control-group and the sandbox is a foreground `apptainer run` child
# of the launcher process, so the stop kills the running agent, not merely the
# launcher's in-memory session registry. Control-plane then polls a session the
# new launcher never heard of, gets the 404 that maps onto
# agenttask.ErrEngineSessionGone, and since PR #914 that is terminal on first
# detection: every in-flight Cowork task died within one 15 second poll of any
# merge. Measured on the box on 2026-08-29: ten stop/start pairs in eight
# hours, and two independent container builds of this binary produced bytes
# identical to the copy already installed there, so every one of those ten
# restarts reinstalled exactly what was already running.
#
# So compare first. The fingerprint covers the four things the running daemon
# actually baked in: the binary, the env file it sources, the entry script that
# wires them together, and since issue #1510 the unit file that defines how it
# is supervised. The unit belongs in the set for both directions: a changed
# restart policy has to actually take effect, and an unchanged one must not buy
# a restart. It is also what makes the one-time migration off the old transient
# unit happen through this same gate rather than through a special case, since
# a box with no unit file installed fingerprints as empty and therefore
# restarts. The SIF is deliberately NOT in it. serve.go reads
# HIVE_AGENT_ENGINE_SIF_PATH once at start-up but only as a path, and
# sandbox.BuildArgv reads the file itself per launch, so a replaced SIF takes
# effect on the next task with no restart, and hashing a multi-gigabyte image
# on every deploy would cost more than the restart it is trying to avoid.
#
# A drain was considered and rejected rather than skipped: Cowork tasks run
# sixteen to twenty-two minutes against this job's 30 minute timeout, so
# waiting for one would trade a dead task for a failed deploy.
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=$XDG_RUNTIME_DIR/bus}"

# Content hashes only. The env file holds the model API key and the internal
# token, so nothing here ever prints or stores the file itself. Paths are
# folded out deliberately (only the first column survives), so a staged
# artifact and its installed counterpart hash the same when their bytes match.
fingerprint_of() {
  local f
  for f in "$@"; do
    # Prints nothing when anything is missing OR unreadable, so a
    # half-finished install can never compare equal to a complete one, and an
    # artifact this account cannot read resolves to "cannot verify, so
    # restart" rather than killing the deploy. Without the -r half, a
    # root-owned installed file (an accidental `sudo` bootstrap, a hand chmod
    # on the box) makes sha256sum exit 1, and `set -o pipefail` turns that into
    # an abort before the restart-and-repair branch is ever reached. Nothing
    # about that condition changes between runs, so every later deploy fails
    # the same way until someone fixes it by hand, while the restart path would
    # have repaired it: `mv -f` over the installed path needs write permission
    # on the directory, not on the file.
    [ -f "$f" ] && [ -r "$f" ] || return 0
  done
  sha256sum "$@" | awk '{print $1}' | sha256sum | cut -d' ' -f1
}

want_fingerprint=$(fingerprint_of "$STAGE_BIN" "$STAGE_ENV" "$STAGE_ENTRY" "$STAGE_UNIT")
# Hash what is actually INSTALLED, not just the note the last run left behind.
# The question that has to be answered here is "does what the daemon is running
# match what I am about to install", and only the installed files can answer
# it. Comparing the staged artifacts against $FINGERPRINT_PATH alone answers
# "does my staging area match a note I wrote last time", which stays true after
# the installed files drift, are corrupted, are edited by hand, or are left
# half-written by an earlier run that failed between two of the three `mv`s.
# Skipping on that is exactly the false confidence this change exists to
# remove, so the installed bytes are re-read on every run.
installed_fingerprint=$(fingerprint_of "$BIN_PATH" "$ENV_FILE" "$RUN_ENTRY" "$UNIT_PATH")
# Still consulted, for the one thing the installed files cannot tell us on
# their own: this is written only after a freshly started unit answered
# /health, so it is the evidence that the running process was started FROM
# those files rather than merely sitting next to them. Without it, a run that
# installed new artifacts and then died before restarting would look identical
# to a healthy box.
recorded_fingerprint=$(cat "$FINGERPRINT_PATH" 2>/dev/null || true)

# ---------------------------------------------------------------------------
# Supervision, installed on EVERY run including the skip path below (issue
# #1510).
#
# This is deliberately not inside the restart branch. Writing a unit file,
# reloading the user manager and enabling a unit do not touch a running
# process: only start, stop and restart do. So a box whose unit was never
# enabled, or whose health timer was removed, is repaired on the next deploy
# without paying the restart that would kill every in-flight Cowork session.
#
# It runs AFTER the fingerprints above are computed, because installing the
# launcher unit before reading the installed one would make that comparison
# answer "does my staging area match my staging area" and the restart would
# never fire. The health probe and its two units are outside the fingerprint
# on purpose in the other direction: changing the watchdog must never restart
# the thing it watches.
# ---------------------------------------------------------------------------
# Staged then renamed, not written in place, for the same reason the binary
# and the env file are. `install` and `>` both truncate first and fill after,
# so a run killed between those two steps leaves a half written probe on disk
# that the timer will happily execute a minute later. `mv` within one
# filesystem is a rename, so a reader sees either the whole old file or the
# whole new one and never a prefix of either. The watchdog is exactly the
# thing that must not be broken by an interrupted deploy, since a broken
# watchdog is silent.
install -m 0755 "$REPO_DIR/scripts/agent-engine-health-probe.sh" "$PROBE_PATH.next"
mv -f "$PROBE_PATH.next" "$PROBE_PATH"
render_unit "$TEMPLATE_DIR/$HEALTH_NAME.service" "$HEALTH_SERVICE_PATH.next"
render_unit "$TEMPLATE_DIR/$HEALTH_NAME.timer" "$HEALTH_TIMER_PATH.next"
mv -f "$HEALTH_SERVICE_PATH.next" "$HEALTH_SERVICE_PATH"
mv -f "$HEALTH_TIMER_PATH.next" "$HEALTH_TIMER_PATH"
mv -f "$STAGE_UNIT" "$UNIT_PATH"
# Explicit on all three, not left to the umask. `umask 022` is in force here,
# which happens to produce 0644, but that is a coincidence of where these
# lines sit rather than a decision, and the umask above has already been
# changed twice in this script. 0644 rather than 0600 deliberately: a unit
# file carries only paths, systemd reads it as this same user either way, and
# the sibling cloudflared.service on the box is 0664, so the tighter mode
# would buy nothing and imply these files hold a secret. They do not; the
# credentials are in $ENV_FILE at 0600, which $RUN_ENTRY sources.
chmod 0644 "$UNIT_PATH" "$HEALTH_SERVICE_PATH" "$HEALTH_TIMER_PATH"
systemctl --user daemon-reload

# `enable` refuses a name currently held by a transient unit, which is exactly
# the state a box carries until the restart below replaces the old
# `systemd-run` unit. That is not a failure: the restart path re-runs enable
# once the transient unit is gone, and a box already on a real unit file takes
# this branch normally.
if [ "$(systemctl --user show "$UNIT_NAME.service" -p Transient --value 2>/dev/null || true)" = "yes" ]; then
  echo "$UNIT_NAME.service is still the pre-#1510 transient unit; the restart below replaces it"
else
  systemctl --user enable "$UNIT_NAME.service" >/dev/null
fi

# Restarting a timer costs nothing and holds no session state, so unlike the
# launcher it is simply re-applied every run. Without it a changed cadence
# would sit on disk unread until the next reboot.
systemctl --user enable "$HEALTH_NAME.timer" >/dev/null
systemctl --user restart "$HEALTH_NAME.timer"

# ---------------------------------------------------------------------------
# The out-of-process observer for the observer.
#
# The timer above cannot report its own death. If it stops, or if the systemd
# user manager stops, the probe never runs, it posts nothing, any firing alert
# lapses, and the silence reads as health. A staleness check that lives only
# inside the timed probe is therefore not a staleness check at all, which is
# what an earlier draft of this change shipped.
#
# So the staleness lane runs from cron, a different scheduler under a different
# daemon, exactly as scripts/backup-box.sh's `--check` watchdog already does on
# this box for the same reason. `--check` reads the stamp and nothing else: it
# does not probe the launcher and never writes the stamp, because a second
# writer would keep the stamp fresh forever and hide the very condition it is
# looking for.
#
# Every fifteen minutes against a fifteen minute threshold, so a timer that
# dies is reported within half an hour at worst.
CRON_LINE="*/15 * * * * $PROBE_PATH --check >/dev/null 2>&1"
CRON_TAG="# hive-agent-engine health staleness watchdog (issue #1510)"
if ! command -v crontab >/dev/null 2>&1; then
  # Not fatal. The systemd timer is the primary lane and it is installed and
  # asserted above; what is missing here is the backstop that notices the
  # primary lane dying. On the demo box crontab exists and this branch never
  # runs. It exists for the hosted-runner job that also executes this
  # installer, where there is no cron and no long-lived box to watch.
  echo "::warning::crontab is not available, so the staleness watchdog is not installed. The five minute timer still runs, but nothing would report the timer itself dying."
else
  # Idempotent, and rewritten every run rather than appended to, so a changed
  # path or cadence takes effect instead of accumulating a second entry.
  # `crontab -l` exits non-zero when the user has no crontab at all, which is
  # the first-install case and not an error.
  existing=$(crontab -l 2>/dev/null || true)
  desired=$(printf '%s\n%s\n' "$CRON_TAG" "$CRON_LINE")
  filtered=$(printf '%s\n' "$existing" | grep -vF "$CRON_TAG" | grep -vF "$PROBE_PATH --check" || true)
  if [ "$(printf '%s\n%s\n' "$filtered" "$desired")" != "$(printf '%s\n' "$existing")" ]; then
    printf '%s\n%s\n' "$filtered" "$desired" | sed '/^$/d' | crontab -
    echo "staleness watchdog: installed cron entry, every 15 minutes"
  else
    echo "staleness watchdog: cron entry already current"
  fi
fi

# The supervision state is ASSERTED, never merely printed.
#
# This used to be `echo "... $(systemctl --user is-enabled ...)"`. Command
# substitution inside a word expansion does not trip `set -e`, and `echo`
# returns its own 0, so a box where `enable` had silently not taken printed
# "supervision: ... is disabled" and the installer exited 0, and the deploy
# went green over a launcher that would not survive a reboot. Reporting a
# state instead of requiring it is the same class of mistake the whole issue
# is about, so this reads the state, requires it, and fails the deploy when it
# is wrong.
assert_supervised() {
  local enabled_state active_state
  enabled_state=$(systemctl --user is-enabled "$UNIT_NAME.service" 2>&1 || true)
  active_state=$(systemctl --user is-active "$HEALTH_NAME.timer" 2>&1 || true)
  if [ "$enabled_state" != "enabled" ]; then
    echo "::error::$UNIT_NAME.service is '$enabled_state', not 'enabled'. It will not start after a reboot, so the launcher would stay down and every agent task would fail. Check: systemctl --user status $UNIT_NAME.service"
    exit 1
  fi
  if [ "$active_state" != "active" ]; then
    echo "::error::$HEALTH_NAME.timer is '$active_state', not 'active'. Nothing would report the launcher being down until someone ran a task. Check: systemctl --user list-timers $HEALTH_NAME.timer"
    exit 1
  fi
  # The cron backstop, asserted on any host that has cron at all. Only warned
  # about where there is no crontab binary, which is the hosted runner rather
  # than the box.
  if command -v crontab >/dev/null 2>&1; then
    if ! crontab -l 2>/dev/null | grep -qF "$PROBE_PATH --check"; then
      echo "::error::the staleness watchdog cron entry is missing. The five minute timer would be unobserved, so its own death would read as health. Check: crontab -l"
      exit 1
    fi
  fi
  echo "supervision: $UNIT_PATH enabled, $HEALTH_NAME.timer active, staleness cron present (all asserted, not assumed)"
}

daemon_healthy() {
  systemctl --user is-active --quiet "$UNIT_NAME.service" || return 1
  [ -S "$SOCKET_PATH" ] || return 1
  # -f, so a 503 is a failure rather than a body this discards. Since issue
  # #1510 /health answers for the ability to LAUNCH and returns 503 with a
  # named failure list when it cannot, and without -f curl exits 0 on that,
  # which would let this skip a restart on a daemon that is reporting itself
  # unable to run a single task.
  curl -fsS --max-time 5 --unix-socket "$SOCKET_PATH" http://localhost/health >/dev/null 2>&1
}

if [ -n "$installed_fingerprint" ] \
  && [ "$installed_fingerprint" = "$want_fingerprint" ] \
  && [ "$recorded_fingerprint" = "$want_fingerprint" ] \
  && daemon_healthy; then
  rm -f "$STAGE_BIN" "$STAGE_ENV" "$STAGE_ENTRY"
  running_pid=$(systemctl --user show "$UNIT_NAME.service" -p MainPID --value 2>/dev/null || echo "?")
  # Read back rather than asserted. A line that prints the word "enabled"
  # unconditionally would report a healthy install on a box where the enable
  # silently did not take, which is the whole failure this change exists to
  # end.
  assert_supervised
  echo "binary: $BIN_PATH (unchanged)"
  echo "agent-engine daemon already runs these exact artifacts; leaving PID $running_pid and its in-flight sessions alone"
  exit 0
fi

mv -f "$STAGE_BIN" "$BIN_PATH"
mv -f "$STAGE_ENV" "$ENV_FILE"
mv -f "$STAGE_ENTRY" "$RUN_ENTRY"
chmod 0600 "$ENV_FILE"
chmod 0700 "$RUN_ENTRY"
echo "binary: $BIN_PATH"

# Cleared BEFORE the restart, not after: a run that dies between the stop and a
# healthy start must not leave a fingerprint claiming these artifacts are live,
# or the next deploy would skip the restart the box actually needs.
rm -f "$FINGERPRINT_PATH"

# ---------------------------------------------------------------------------
# Start the launcher (issue #1510).
#
# The `stop` is what frees the name if the box is still carrying the old
# transient unit: transient units live in /run/user/<uid>/systemd/transient,
# which takes precedence over ~/.config/systemd/user, so the file installed
# above stays shadowed until the transient one is gone. Stopping it also
# collects it, since it was created with CollectMode=inactive-or-failed.
#
# The daemon-reload afterwards is what makes the manager pick up the on-disk
# unit in that same run rather than a deploy later, and the enable is repeated
# here because the unconditional block above deliberately skips it while a
# transient unit still holds the name.
#
# It keeps running after the CI job that started it exits (the user manager
# owns it, not the runner), it now restarts on ANY exit rather than only on
# failure, it survives a reboot because it is enabled and this user has
# lingering on, and it still gets a delegated cgroup for free.
# ---------------------------------------------------------------------------
systemctl --user stop "$UNIT_NAME.service" 2>/dev/null || true
rm -f "$SOCKET_PATH"
systemctl --user daemon-reload
systemctl --user enable "$UNIT_NAME.service" >/dev/null
systemctl --user start "$UNIT_NAME.service"

for _ in $(seq 1 30); do
  [ -S "$SOCKET_PATH" ] && break
  sleep 1
done
# --fail-with-body rather than plain -f: a 503 here carries the named list of
# broken preconditions (a deleted image, a missing packs dir, apptainer gone
# from PATH), and that list is the whole diagnosis. Plain -f discards the body
# and would turn a precise failure into "exit 22".
if ! curl -sS --fail-with-body --max-time 5 --unix-socket "$SOCKET_PATH" http://localhost/health; then
  echo
  echo "::error::agent-engine daemon did not answer /health on $SOCKET_PATH"
  systemctl --user status "$UNIT_NAME.service" --no-pager --lines=40 || true
  exit 1
fi
echo
# Only now, with the new unit answering, is it true that these artifacts are
# what is running. The next deploy compares against this and skips the restart
# when nothing changed.
# 0600 like $ENV_FILE and 0700 like $RUN_ENTRY and $SOCKET_DIR: this is the
# only file the script leaves in $RUNTIME_DIR that the surrounding umask 022
# would otherwise make world readable, and nothing outside this script and the
# unit it starts has any business reading it. The umask is what stops the file
# being created 0644 and narrowed a moment later, the way $STAGE_ENV and
# $STAGE_ENTRY are already created under `umask 077` rather than fixed up
# afterwards. The chmod stays for the run that finds the file already there
# from an older version of this script, since `>` truncates without touching
# the mode.
(umask 077; printf '%s\n' "$want_fingerprint" > "$FINGERPRINT_PATH")
chmod 0600 "$FINGERPRINT_PATH"
echo "agent-engine daemon healthy on $SOCKET_PATH"
assert_supervised
