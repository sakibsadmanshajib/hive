#!/usr/bin/env bash
# Does the shipped bundle still carry the Open WebUI admin panel?
#
# Counted in both images rather than asserted once. The discriminator is a route
# string from lib/components/admin/Settings.svelte's own tab table, which
# reaches the built output only through an import chain that starts at the
# (app)/admin route tree this change deletes: with the routes gone the component
# has no importer and the bundler drops it.
set -uo pipefail
out=$1
: > "$out"

count() {
  local image=$1 label=$2 needle=$3
  local n
  n=$(docker run --rm --entrypoint sh "$image" -c \
    "grep -rlF '$needle' /app/build/_app/immutable 2>/dev/null | wc -l" | tr -d '\r')
  printf '%-34s %-42s %s chunk(s)\n' "$label" "$needle" "$n" | tee -a "$out"
}

echo "# admin panel presence in the built bundle" | tee -a "$out"
echo "# before: hive-open-webui:v0.10.2-branded, a fork build from main" | tee -a "$out"
echo "#         (it carries data-hive-nav, so it is our own frontend and not" | tee -a "$out"
echo "#          upstream's prebuilt bundle)" | tee -a "$out"
echo "# after:  hive-owui-adminlock:after, this branch's frontend over the same base" | tee -a "$out"
echo | tee -a "$out"
count hive-open-webui:v0.10.2-branded "before (main)" "/admin/settings/code-execution"
count hive-owui-adminlock:after       "after (this branch)" "/admin/settings/code-execution"
echo | tee -a "$out"
echo "# control: the two strings the Dockerfile already asserted, unchanged" | tee -a "$out"
count hive-open-webui:v0.10.2-branded "before (main)" "data-hive-nav"
count hive-owui-adminlock:after       "after (this branch)" "data-hive-nav"
count hive-open-webui:v0.10.2-branded "before (main)" '"/workspace/models"'
count hive-owui-adminlock:after       "after (this branch)" '"/workspace/models"'
echo | tee -a "$out"
echo "# the Settings dialog's own link target, as a second independent needle" | tee -a "$out"
count hive-open-webui:v0.10.2-branded "before (main)" "Admin Settings"
count hive-owui-adminlock:after       "after (this branch)" "Admin Settings"
