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

mkdir -p "$WORK/hive"
cp "$SRC"/*.ts "$WORK"/hive/

# The settings declutter guard pins the rendered surface of four chat
# components by reading their sources. The scratch tree therefore keeps the
# same hive-plus-components shape the sources have in the real tree, so the
# same relative URLs resolve in both places.
COMPONENT_SRC="$ROOT/vendor/open-webui/src/lib/components"
for rel in \
	chat/SettingsModal.svelte \
	chat/ModelSelector/Selector.svelte \
	chat/Settings/Account.svelte \
	chat/Settings/Advanced/AdvancedParams.svelte
do
	mkdir -p "$WORK/components/${rel%/*}"
	cp "$COMPONENT_SRC/$rel" "$WORK/components/$rel"
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
