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
cd "$WORK"

# Pinned major so a vitest release cannot change the meaning of a green run.
npx --yes vitest@2 run
