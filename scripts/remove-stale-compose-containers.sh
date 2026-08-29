#!/usr/bin/env bash
# Removes containers for compose services outside the deploy's active profile
# set (issue #967).
#
# `docker compose up --remove-orphans` removes a container whose service is
# gone from the compose file entirely. It does not touch a container whose
# service still exists but sits in a profile the deploy did not enable, so
# moving a service behind a profile stops it being STARTED and never stops it
# RUNNING. Nothing else in deploy-demo-box.yml closed that gap, which made a
# profile gate look like a control and behave like a comment.
#
# `web-console` is what proved it. PR #605 moved the `next dev` server into the
# `dev` profile on 2026-07-31, and `hive-web-console-1` was still up four weeks
# later on 2026-08-29: restart policy `unless-stopped`, publishing
# `0.0.0.0:3000`, answering /auth/sign-in in 5.56 seconds from a development
# build against real credentials. That service's own comment in
# docker-compose.yml describes the port it publishes as an unauthenticated
# LAN-reachable surface, because Docker's published ports bypass ufw's
# default-deny. Roughly thirty deploys ran in that window and every one of them
# left it alone.
#
# `docker compose config --services` under the deploy's own flags is exactly
# the set of services the deploy is allowed to run, so a container in the
# project whose service is absent from that set is stale by definition. No
# per-service list lives here, so a future service gated out of a profile is
# evicted by the same mechanism without anyone remembering to add it.
#
# Blast radius if the selection is ever wrong: `docker rm -f` is run WITHOUT
# `-v`, so it removes the container and never a volume, named or anonymous. No
# data store can be destroyed by this script even when it names the wrong
# container; the cost is a restart. That matters because the demo box has no
# off-box backup of any production data store.
#
# Usage:
#   remove-stale-compose-containers.sh [--dry-run]
# Environment:
#   HIVE_COMPOSE_FLAGS  required, the same flag string the deploy's `up` uses.
#   COMPOSE_PROJECT     optional, defaults to `hive`.
set -euo pipefail

dry_run=0
if [ "${1:-}" = "--dry-run" ]; then
  dry_run=1
elif [ -n "${1:-}" ]; then
  echo "unknown argument: $1" >&2
  exit 2
fi

# No apostrophe in this message: bash parses the word of a ${var:?word}
# expansion for quotes even inside double quotes, so one would swallow the rest
# of the file and fail with an unmatched-quote error 60 lines further down.
: "${HIVE_COMPOSE_FLAGS:?HIVE_COMPOSE_FLAGS must be set to the compose flags this deploy uses}"

# Fail closed. An empty list would otherwise read as "no service is active" and
# select the entire deployment for removal, so it is an error, never a licence.
# Compose writes its interpolation warnings to stderr, so a warning cannot be
# mistaken for a service name; were one ever to reach stdout the error would be
# in the safe direction, since a longer active list removes less.
# shellcheck disable=SC2086 # HIVE_COMPOSE_FLAGS is a flag string and must split
active=$(docker compose $HIVE_COMPOSE_FLAGS config --services)
if [ -z "$active" ]; then
  echo "::error::docker compose config --services returned nothing; refusing to remove any container" >&2
  exit 1
fi

# The project name is asked of compose, never assumed. `COMPOSE_PROJECT_NAME`
# in the box's untracked .env overrides the `name:` in the compose file, and a
# hardcoded `hive` would then match no container at all: the filter would come
# back empty, the script would report "removed: 0" and exit green, and the
# stale container this exists to evict would go on running behind a passing
# step. A silent no-op is the one outcome worse than a loud failure here.
first_container=$(docker compose $HIVE_COMPOSE_FLAGS ps --quiet | head -1)
if [ -z "$first_container" ]; then
  echo "::error::this compose project has no running containers, so the project name cannot be resolved; refusing to remove anything" >&2
  exit 1
fi
project=$(docker inspect \
  -f '{{index .Config.Labels "com.docker.compose.project"}}' "$first_container")
if [ -z "$project" ]; then
  echo "::error::could not read the compose project label off $first_container; refusing to remove anything" >&2
  exit 1
fi

# Captured before the loop rather than piped into it: a pipeline runs its
# right-hand side in a subshell, where a failing `docker rm` could not fail
# this script. Compose service and container names are restricted to
# [a-zA-Z0-9._-], so neither can contain whitespace or `=` and the word split
# below and the `service=container` split are both unambiguous.
#
# `oneoff=False` excludes `docker compose run` containers. Those carry the same
# project and service labels as a long-running service but are somebody's
# in-flight one-shot command, most often a `toolchain` test run, and killing
# one mid-flight would fail a job for a reason nobody could trace back here.
running=$(docker ps \
  --filter "label=com.docker.compose.project=$project" \
  --filter "label=com.docker.compose.oneoff=False" \
  --format '{{.Label "com.docker.compose.service"}}={{.Names}}')

# An upper bound on the blast radius. Every observed instance of this fault is
# a single container left behind by a single profile change, so a run that
# wants to remove more than a handful has almost certainly resolved the active
# set wrongly rather than found four separate stale services. On a box with no
# off-box backup, stopping to be told is worth more than converging in one
# pass: raise the bound deliberately if a real deploy ever needs it.
MAX_REMOVALS=${MAX_REMOVALS:-3}

removed=0
stale=""
for entry in $running; do
  service="${entry%%=*}"
  container="${entry#*=}"
  # A container with no compose service label is not this script's to judge.
  [ -n "$service" ] && [ -n "$container" ] && [ "$service" != "$container" ] || continue
  # A herestring rather than `printf | grep`: under `pipefail` a `grep -q` that
  # exits on its first match can leave the writer killed by SIGPIPE, making the
  # whole pipeline non-zero and sending an ACTIVE service down the delete
  # branch. `-x` is what keeps `web-console` from matching `web-console-prod`,
  # and `--` protects a service name that begins with a dash.
  if grep -qxF -- "$service" <<<"$active"; then
    continue
  fi
  stale="$stale $container=$service"
  removed=$((removed + 1))
done

if [ "$removed" -gt "$MAX_REMOVALS" ]; then
  echo "::error::$removed containers look stale, above the bound of $MAX_REMOVALS:$stale" >&2
  echo "::error::that many at once usually means the active service list resolved wrongly, not that this many services went stale; nothing was removed" >&2
  exit 1
fi

for pair in $stale; do
  container="${pair%%=*}"
  service="${pair#*=}"
  if [ "$dry_run" -eq 1 ]; then
    echo "would remove $container: service $service is not in this deploy's profile set"
  else
    echo "removing $container: service $service is not in this deploy's profile set"
    docker rm -f "$container" >/dev/null
  fi
done

if [ "$dry_run" -eq 1 ]; then
  echo "stale containers that would be removed: $removed"
else
  echo "stale containers removed: $removed"
fi
