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

# The whole directory, recursively, rather than a glob per extension. A glob
# would silently stop covering a component the day someone put one in a
# subdirectory, and a check that quietly narrows is worse than no check. vitest
# and the compile pass both recurse, so structure is preserved rather than
# flattened, and both see everything that is actually there. The .svelte files
# cost the unit run nothing: vitest's default include globs match test files
# only.
cp -R "$SRC"/. "$WORK"/
cp "$ROOT/scripts/owui-hive-svelte-compile-check.mjs" "$WORK"/

# The vendored lockfile travels too, so the compile pass installs the EXACT
# svelte version the image build resolves rather than whatever `svelte@5`
# points at today. A major-only pin would let this check compile with a
# different 5.x than deploy/docker/Dockerfile.open-webui does, which is a fresh
# way to get a green check and a red deploy. Read inside the container, so this
# still needs no host node, per the Docker-only testing contract above.
cp "$ROOT/vendor/open-webui/package-lock.json" "$WORK"/owui-package-lock.json

cd "$WORK"

# Runs in a pinned node image rather than on host node, per CLAUDE.md's
# Docker-only testing contract: a contributor with no host node still gets this
# check, and the node version cannot drift between a laptop and CI. Pinned
# vitest major on top, so neither a node release nor a vitest release can change
# the meaning of a green run. The scratch directory is the only mount, and the
# container is given the caller's uid so the npx cache it writes there is not
# left root owned on the host.
# Two passes in one container: the unit tests, then a Svelte compile of every
# component in the tree. The compile pass is what stops a component that does
# not build from merging green, which happened on 2026-08-23 and only surfaced
# in the deploy-demo-box image build, four minutes into a Docker build and hours
# after merge.
docker run --rm \
  -v "$WORK:/work" -w /work \
  -u "$(id -u):$(id -g)" \
  -e HOME=/work \
  node:22-alpine3.20 \
  sh -c 'set -eu
    npx --yes vitest@2 run
    svelte_version=$(node -e "
      const lock = require(\"/work/owui-package-lock.json\");
      const entry = lock.packages && lock.packages[\"node_modules/svelte\"];
      if (!entry || !entry.version) {
        console.error(\"svelte absent from vendor/open-webui/package-lock.json\");
        process.exit(1);
      }
      process.stdout.write(entry.version);
    ")
    echo "compiling components with svelte@$svelte_version, the version the image build resolves"
    npm install --no-save --no-audit --no-fund --loglevel=error "svelte@$svelte_version"
    node owui-hive-svelte-compile-check.mjs .'
