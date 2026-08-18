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
# Idempotent: safe to run on every deploy. Prints no secret values.
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

for var in HIVE_AGENT_ENGINE_LLM_MODEL HIVE_AGENT_ENGINE_LLM_BASE_URL \
           HIVE_AGENT_ENGINE_LLM_API_KEY CONTROL_PLANE_INTERNAL_TOKEN; do
  if [ -z "${!var:-}" ]; then
    echo "::error::$var is required (value never printed)"
    exit 1
  fi
done

command -v apptainer >/dev/null || { echo "::error::apptainer is not installed on this host"; exit 1; }

mkdir -p "$RUNTIME_DIR/bin" "$SOCKET_DIR" "$RUNTIME_DIR/workspaces" "$RUNTIME_DIR/sessions"
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
  golang:1.24-alpine \
  go build -o /out/agent-engine ./apps/agent-engine/cmd/agent-engine
# `go build` already emits 0755, so this is belt and braces for an odd umask.
# It is allowed to fail: under rootful Docker (every GitHub-hosted runner) the
# build ran as root and the binary is not ours to chmod, which used to abort
# the install with "Operation not permitted" even though the file was already
# correct. What has to hold is that it ends up executable, so assert that
# rather than trusting either the chmod or the builder.
chmod 0755 "$BIN_PATH" 2>/dev/null || true
if [ ! -x "$BIN_PATH" ]; then
  echo "::error::$BIN_PATH is not executable after the build"
  exit 1
fi
echo "binary: $BIN_PATH"

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
} > "$ENV_FILE"
chmod 0600 "$ENV_FILE"

# Apptainer enforces this sandbox's --memory/--cpus/--pids-limit through
# rootless cgroups, which need a systemd user session to delegate them. A
# plain `nohup` from a CI step has neither variable set and the launch dies
# with "cannot use cgroups - DBUS_SESSION_BUS_ADDRESS is not set", measured on
# this box. Running under the user manager (below) supplies both; they are
# pinned here as well so a hand-run daemon behaves identically.
cat > "$RUN_ENTRY" <<EOF
#!/usr/bin/env bash
set -euo pipefail
export XDG_RUNTIME_DIR="\${XDG_RUNTIME_DIR:-/run/user/\$(id -u)}"
export DBUS_SESSION_BUS_ADDRESS="\${DBUS_SESSION_BUS_ADDRESS:-unix:path=\$XDG_RUNTIME_DIR/bus}"
set -a
. "$ENV_FILE"
set +a
exec "$BIN_PATH" -serve "$SOCKET_PATH"
EOF
chmod 0700 "$RUN_ENTRY"
umask 022

# ---------------------------------------------------------------------------
# The unit. A transient systemd user service, so it keeps running after the
# CI job that started it exits (the runner reaps its own orphans), restarts on
# failure, and gets a delegated cgroup for free.
# ---------------------------------------------------------------------------
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=$XDG_RUNTIME_DIR/bus}"
systemctl --user stop "$UNIT_NAME.service" 2>/dev/null || true
rm -f "$SOCKET_PATH"
systemd-run --user --unit="$UNIT_NAME" --collect \
  --property=Restart=on-failure --property=RestartSec=5 \
  "$RUN_ENTRY"

for _ in $(seq 1 30); do
  [ -S "$SOCKET_PATH" ] && break
  sleep 1
done
if ! curl -sS --max-time 5 --unix-socket "$SOCKET_PATH" http://localhost/health; then
  echo
  echo "::error::agent-engine daemon did not answer /health on $SOCKET_PATH"
  systemctl --user status "$UNIT_NAME.service" --no-pager --lines=40 || true
  exit 1
fi
echo
echo "agent-engine daemon healthy on $SOCKET_PATH"
