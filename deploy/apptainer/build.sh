#!/usr/bin/env bash
#
# Build the agent-engine Apptainer image (agent-engine.sif) on a host that has
# apptainer installed. This is the demo/production host counterpart to the
# .github/workflows/agent-engine-sif.yml CI job.
#
# Usage:
#   deploy/apptainer/build.sh                 # -> deploy/apptainer/agent-engine.sif
#   deploy/apptainer/build.sh /opt/hive/agent-engine.sif
#   make agent-sif                            # same, from the repo root
#
# The script cd's into its own directory before building so the def file's
# ../../ %files sources (vendor/openhands, apps/agent-engine/packs) resolve to
# the repo root regardless of where you invoke it from.
#
# Privilege: building a docker-bootstrap image needs either real root or a
# rootless setup with fakeroot. If a plain `apptainer build` fails on your host,
# re-run with sudo, or pass fakeroot via the knob below:
#   sudo deploy/apptainer/build.sh
#   APPTAINER_BUILD_ARGS=--fakeroot deploy/apptainer/build.sh
set -euo pipefail

cd "$(dirname "$0")"

DEF="agent-engine.def"
OUT="${1:-agent-engine.sif}"

if ! command -v apptainer >/dev/null 2>&1; then
  echo "error: apptainer is not installed or not on PATH." >&2
  echo "See https://apptainer.org/docs/admin/main/installation.html" >&2
  exit 1
fi

echo "Building ${OUT} from ${DEF} (cwd: $(pwd)) ..."
# shellcheck disable=SC2086 # APPTAINER_BUILD_ARGS is an intentional word-split knob.
apptainer build ${APPTAINER_BUILD_ARGS:-} "${OUT}" "${DEF}"

ABS="$(cd "$(dirname "${OUT}")" && pwd)/$(basename "${OUT}")"
echo
echo "Built: ${ABS}"
echo "Point agent-engine at it with:"
echo "  export HIVE_AGENT_SIF_PATH=${ABS}"
