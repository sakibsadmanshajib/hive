#!/usr/bin/env sh
# Runs the Hive authored unit tests inside the vendored chat front end.
#
# Everything under vendor/open-webui/src/lib/hive is ours and depends on nothing
# but vitest and its own siblings, which is what makes this possible: the files
# are copied into a scratch directory and run there, without a full npm install
# of the chat front end.
#
# `npm run test:frontend -- --run` (Dockerfile.open-webui, frontend build stage)
# ALSO runs these same files in place, against the real tree with the real
# node_modules, and is a genuine build-time gate: a failing test here fails the
# image build. This script exists for local/CI iteration speed where a full
# frontend npm install is too slow to want on every change, not because the
# in-place run is unreachable. Both runs execute the identical test sources,
# so the scratch tree's shape mirrors the real tree's shape exactly
# (src/lib/hive, src/lib/components, src/routes), and every relative import a
# test file uses (../components/..., ../../routes/...) resolves the same way
# in both places. Getting this mirroring wrong is silent until the Docker
# build's in-place run catches it; verify locally with this script AND with a
# frontend image build before trusting either alone.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
SRC="$ROOT/vendor/open-webui/src/lib/hive"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/lib/hive"
# The app template too: early-redirect.test.ts executes the shipped bytes of
# the inline signed-out fast-exit script by reading src/app.html out of the
# mirrored tree, so the template must travel with the sources it pins.
cp "$ROOT/vendor/open-webui/src/app.html" "$WORK"/app.html
# The whole Hive directory, recursively, rather than a glob per extension. A
# glob covers only what sits at the top of it today and would silently stop
# covering a component the day someone put one in a subdirectory, which is the
# same quiet narrowing that let AgentSchedules.svelte reach the deploy
# uncompiled. The .svelte files cost the unit run nothing: vitest's default
# include globs match test files only. Structure is preserved rather than
# flattened, so the mirroring described above still holds.
cp -R "$SRC"/. "$WORK"/lib/hive/

# The settings declutter guard (plus the settings retitle/Usage-tab guard)
# pins the rendered surface of chat components, plus the layout/page files
# that also forward directConnections, by reading their sources.
COMPONENT_SRC="$ROOT/vendor/open-webui/src/lib/components"
for rel in \
	chat/SettingsModal.svelte \
	chat/ModelSelector/Selector.svelte \
	chat/Settings/Account.svelte \
	chat/Settings/General.svelte \
	chat/Settings/Usage.svelte \
	chat/Settings/Advanced/AdvancedParams.svelte \
	chat/MessageInput.svelte \
	chat/Chat.svelte \
	chat/Placeholder.svelte \
	chat/Settings/Interface.svelte
do
	mkdir -p "$WORK/lib/components/${rel%/*}"
	cp "$COMPONENT_SRC/$rel" "$WORK/lib/components/$rel"
done

ROUTES_SRC="$ROOT/vendor/open-webui/src/routes"
for rel in \
	+layout.svelte \
	"(app)/+layout.svelte" \
	"s/[id]/+page.svelte"
do
	mkdir -p "$WORK/routes/$(dirname -- "$rel")"
	cp "$ROUTES_SRC/$rel" "$WORK/routes/$rel"
done

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
# container is given the caller's uid so the npm cache it writes there is not
# left root owned on the host.
# Two passes in one container: the unit tests, then a Svelte compile of every
# component under lib/hive. The compile pass is what stops a component that does
# not build from merging green, which happened on 2026-08-23 and only surfaced
# in the deploy-demo-box image build, four minutes into a Docker build and hours
# after merge.
#
# Scoped to lib/hive, not the whole scratch tree. The upstream components and
# routes copied in above are fixtures the declutter guard READS as text; they
# are compiled by the image build with the real preprocessor chain and their
# imports resolved against the real node_modules, neither of which exists here,
# so compiling them out of context would test this script rather than them.
docker run --rm \
  -v "$WORK:/work" -w /work \
  -u "$(id -u):$(id -g)" \
  -e HOME=/work \
  node:22-alpine3.20 \
  sh -c 'set -eu
    # Coverage provider pinned to the same major as vitest so the pair cannot
    # drift. Both are installed into the scratch tree rather than pulled
    # through npx: vitest resolves @vitest/coverage-v8 relative to the project
    # root it runs from, and packages fetched through separate npx prefixes are
    # invisible to that lookup. Same pattern as the svelte install below.
    # The text reporter prints the per-file table plus an All files total line:
    # advisory measurement for this scratch-tree run, no thresholds.
    # Scoped to lib/hive, not all of lib: the upstream components and routes
    # copied in above are text fixtures the declutter guard reads, not code
    # this suite executes, so including them would drag the total down with
    # permanently-zero rows.
    npm install --no-save --no-audit --no-fund --loglevel=error vitest@2 @vitest/coverage-v8@2
    npx vitest run --coverage --coverage.include="lib/hive/**" --coverage.reporter=text
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
    node owui-hive-svelte-compile-check.mjs lib/hive'
