#!/usr/bin/env bash
# Isolates this worktree's `docker compose` containers from every other
# worktree/checkout on the same machine (hive issue #1242).
#
# Compose keys container identity on the project name (COMPOSE_PROJECT_NAME,
# or the top-level `name:` in docker-compose.yml as fallback, currently
# "hive"). Every worktree checkout of this repo shares that same fallback,
# so `docker compose run --build web-console ...` in one worktree can
# recreate `hive-control-plane-1` that actually belongs to a different
# worktree's running stack.
#
# What this writes, and why both files (either alone is not enough, because
# the documented commands in CLAUDE.md read different env files):
#   - deploy/docker/.env      read by compose's DEFAULT .env discovery, used
#                              by commands that pass no --env-file (e.g. the
#                              `docker compose run --build web-console ...`
#                              frontend build/test commands).
#   - <repo-root>/.env        read explicitly via `--env-file ../../.env` by
#                              every other documented command (local/chat/
#                              agent/cloud/enterprise profiles, sdk-tests).
#                              Only touched if it already exists (created by
#                              this repo's own `cp .env.example .env` step);
#                              never force-created here.
# An explicit --env-file argument fully replaces compose's default .env
# discovery (confirmed empirically, see PR description), which is why one
# file cannot cover both command shapes.
#
# No-op guarantee for the canonical checkout: the demo box and every CI
# checkout of this repo sit in a directory literally named "hive" (that is
# what --env-file ../../.env is calibrated for on the demo box today per
# .wolf/cerebrum.md). This script short-circuits before touching a single
# file when the worktree root's basename is "hive", so the demo box and any
# plain single-checkout dev setup keep the exact "hive" project name they
# have always had.
#
# Run this AFTER copying any .env into the worktree, never before. The repo-root
# .env below is written only when it already exists, and it is the file
# `--env-file ../../.env` reads, so running this first either leaves it without
# a COMPOSE_PROJECT_NAME at all or lets a later `cp <somewhere>/.env .env`
# overwrite the line. Either way the worktree silently rejoins project "hive"
# and the next `docker compose up` recreates the canonical checkout's
# containers.
#
# Usage:
#   scripts/set-compose-project-name.sh          write the isolation files
#   scripts/set-compose-project-name.sh --check  verify no live collision,
#                                                 read-only, no writes
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
slug_raw="$(basename "$repo_root")"

if [[ "$slug_raw" == "hive" ]]; then
  echo "canonical checkout ('hive'): compose project name stays 'hive', nothing written."
  exit 0
fi

# ponytail: basename-only collision (two differently-parented checkouts both
# literally named "hive") is a known ceiling of the no-op check above; not a
# real risk on this machine (single canonical path), add a full-path
# comparison if that ever changes.

sanitize() {
  local s="${1,,}"
  s="${s//[^a-z0-9]/-}"
  while [[ "$s" == *--* ]]; do s="${s//--/-}"; done
  s="${s#-}"
  s="${s%-}"
  [[ "$s" =~ ^[a-z0-9] ]] || s="w-$s"
  printf '%s' "$s"
}

slug="$(sanitize "$slug_raw")"
path_hash="$(printf '%s' "$repo_root" | sha1sum | cut -c1-8)"
project_name="hive-${slug}-${path_hash}"
compose_dir="$repo_root/deploy/docker"

if [[ "${1:-}" == "--check" ]]; then
  if ! command -v docker >/dev/null 2>&1; then
    echo "ERROR: docker not found on PATH, cannot check for a project-name collision." >&2
    exit 1
  fi
  ps_output="$(docker ps -a \
    --filter "label=com.docker.compose.project=${project_name}" \
    --format '{{.Label "com.docker.compose.project.working_dir"}}')" || {
    echo "ERROR: 'docker ps' failed, cannot check for a project-name collision (is the docker daemon running?)." >&2
    exit 1
  }
  colliding_dir="$(printf '%s\n' "$ps_output" | sort -u | grep -vxF "$compose_dir" || true)"
  if [[ -n "$colliding_dir" ]]; then
    echo "ERROR: compose project '${project_name}' already has containers from a different working directory:" >&2
    echo "$colliding_dir" >&2
    echo "This is hive issue #1242 (project-name collision). Re-run scripts/set-compose-project-name.sh in the OTHER checkout to give it its own name, or stop its stack first: docker compose -p ${project_name} down" >&2
    exit 1
  fi
  echo "ok: '${project_name}' has no containers outside this worktree ($compose_dir)."
  exit 0
fi

upsert_line() {
  local file="$1"
  mkdir -p "$(dirname "$file")"
  touch "$file"
  if grep -q '^COMPOSE_PROJECT_NAME=' "$file"; then
    sed -i "s/^COMPOSE_PROJECT_NAME=.*/COMPOSE_PROJECT_NAME=${project_name}/" "$file"
  else
    printf '\nCOMPOSE_PROJECT_NAME=%s\n' "$project_name" >> "$file"
  fi
}

upsert_line "$compose_dir/.env"

if [[ -f "$repo_root/.env" ]]; then
  upsert_line "$repo_root/.env"
fi

echo "compose project for this worktree: ${project_name}"
