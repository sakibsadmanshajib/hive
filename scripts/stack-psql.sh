#!/usr/bin/env bash
# stack-psql.sh -- run psql against the database the demo box's stack actually
# uses, from a host that cannot reach it directly.
#
# Why this exists
# ---------------
# Until the self-hosted Supabase cutover (PR #986) every database this repo's
# workflows touched was a hosted Supavisor pooler on a public hostname, so
# `psql` on the runner's own host resolved it and connected. The self-hosted
# data plane has no published port at all (see docker-compose.enterprise.yml:
# "No host port binding: all access is via the internal compose network"), and
# its hostname `supabase-db` is a compose-network name. A host-side psql
# therefore fails with
#
#     could not translate host name "supabase-db" to address: Name has no
#     usable address
#
# which is exactly how the deploy job's model-catalog price assertion broke on
# the first deploy after the cutover. That step already ran psql in a container
# (`docker run --rm postgres:16-alpine psql ...`), but on the default bridge
# network, where the name does not resolve either.
#
# This is the single place that answers "how does a command on the box reach
# that database". Every caller goes through it rather than inventing its own
# docker invocation, because a second invocation is a second answer, and the
# one that is wrong fails in a way that reads like a broken database.
#
# What it does
# ------------
# Runs the official psql client in a throwaway container attached to the
# stack's compose network, with the repository bind-mounted read-only at its
# own absolute path so an absolute `-f /path/to/migration.sql` resolves to the
# same file inside. libpq environment variables are passed through, so callers
# keep using PGHOST/PGPORT/PGUSER/PGDATABASE/PGPASSWORD and no DSN is built
# here (DSN parameters are per-driver, and a pgx-only parameter in a libpq
# value is rejected outright; that mistake has already crash-looped a
# container).
#
# The container's working directory is the repository root, not the caller's.
# Pass absolute paths to `-f`. A relative one resolves against the repo root
# and fails loudly with "No such file or directory" rather than silently
# reading the wrong file.
#
# Usage
# -----
#   scripts/stack-psql.sh -tAc 'SELECT 1'                 # libpq env vars
#   scripts/stack-psql.sh "$SUPABASE_DB_POOL_URL_LIBPQ" -tAc 'SELECT 1'
#   PSQL_BIN=scripts/stack-psql.sh scripts/apply-migrations.sh
#
# HIVE_STACK_NETWORK overrides the network. Its default is derived, not
# guessed: docker-compose.enterprise.yml pins `name: hive`, and compose names
# a project's implicit network `<project>_default`. scripts/
# test_selfhost_supabase_seam.py fails if that project name changes without
# this default following it.

set -euo pipefail

network="${HIVE_STACK_NETWORK:-hive_default}"
# Same client the deploy job has been using for this query all along. Not
# pinned by digest deliberately: it is a throwaway client that reads nothing
# from the image but the psql binary, and a stale pin here would be one more
# thing to chase when the server moves.
image="${HIVE_PSQL_IMAGE:-postgres:16-alpine}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! docker network inspect "$network" >/dev/null 2>&1; then
  echo "stack-psql: docker network '$network' does not exist, so there is no stack to reach." >&2
  echo "stack-psql: bring the stack up first, or set HIVE_STACK_NETWORK to the right network." >&2
  docker network ls --format '  {{.Name}}' >&2
  exit 1
fi

# Unset variables are simply not forwarded by `-e NAME`, so listing them all is
# safe whether or not the caller exported them.
exec docker run --rm -i \
  --network "$network" \
  --volume "$repo_root:$repo_root:ro" \
  --workdir "$repo_root" \
  -e PGHOST -e PGPORT -e PGUSER -e PGDATABASE -e PGPASSWORD \
  -e PGSSLMODE -e PGOPTIONS -e PGCONNECT_TIMEOUT -e PGAPPNAME \
  "$image" psql "$@"
