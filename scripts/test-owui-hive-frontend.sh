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
cp "$SRC"/*.ts "$WORK"/lib/hive/

# The settings declutter guard pins the rendered surface of chat components,
# plus the layout/page files that also forward directConnections, by reading
# their sources.
COMPONENT_SRC="$ROOT/vendor/open-webui/src/lib/components"
for rel in \
	chat/SettingsModal.svelte \
	chat/ModelSelector/Selector.svelte \
	chat/Settings/Account.svelte \
	chat/Settings/Advanced/AdvancedParams.svelte
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
cd "$WORK"

# Runs in a pinned node image rather than on host node, per CLAUDE.md's
# Docker-only testing contract: a contributor with no host node still gets this
# check, and the node version cannot drift between a laptop and CI. Pinned
# vitest major on top, so neither a node release nor a vitest release can change
# the meaning of a green run. The scratch directory is the only mount, and the
# container is given the caller's uid so the npx cache it writes there is not
# left root owned on the host.
docker run --rm \
  -v "$WORK:/work" -w /work \
  -u "$(id -u):$(id -g)" \
  -e HOME=/work \
  node:22-alpine3.20 \
  npx --yes vitest@2 run
