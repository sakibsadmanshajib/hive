#!/usr/bin/env sh
# Runs the Hive authored unit tests inside the vendored chat front end.
#
# Everything under vendor/open-webui/src/lib/hive is ours and depends on nothing
# but vitest and its own siblings, which is what makes this possible: the files
# are copied into a scratch directory and run there. Running vitest in place
# instead makes it resolve vendor/open-webui's own config and dependency tree,
# which is installed only inside the image build, so the tests would need a full
# npm install of the chat front end to run at all. They were therefore running
# nowhere: package.json's `test:frontend` script is referenced only by an
# upstream workflow file that this repository never executes, so the sign in
# redirect decision, the part of this front end that can lock every user out,
# had no pre-merge check on it.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
SRC="$ROOT/vendor/open-webui/src/lib/hive"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

cp "$SRC"/*.ts "$WORK"/
# The .svelte components come too, for the compile pass below. vitest ignores
# them (its default include globs match only test files), so they cost the unit
# run nothing.
cp "$SRC"/*.svelte "$WORK"/
cp "$ROOT/scripts/owui-hive-svelte-compile-check.mjs" "$WORK"/
cd "$WORK"

# Runs in a pinned node image rather than on host node, per CLAUDE.md's
# Docker-only testing contract: a contributor with no host node still gets this
# check, and the node version cannot drift between a laptop and CI. Pinned
# vitest major on top, so neither a node release nor a vitest release can change
# the meaning of a green run. The scratch directory is the only mount, and the
# container is given the caller's uid so the npx cache it writes there is not
# left root owned on the host.
# Two passes in one container: the unit tests, then a Svelte compile of every
# component in that directory. The compile pass is what stops a component that
# does not build from merging green, which happened on 2026-08-23 and only
# surfaced in the deploy-demo-box image build. The svelte major is pinned to
# the one vendor/open-webui/package.json declares, so this compiles what the
# image build will compile.
docker run --rm \
  -v "$WORK:/work" -w /work \
  -u "$(id -u):$(id -g)" \
  -e HOME=/work \
  node:22-alpine3.20 \
  sh -c 'npx --yes vitest@2 run \
    && npm install --no-save --no-audit --no-fund --loglevel=error svelte@5 \
    && node owui-hive-svelte-compile-check.mjs .'
