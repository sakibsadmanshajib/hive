#!/usr/bin/env bash
# Refuse to start work on the demo box without disk headroom. Issue #1098.
#
# Called as the FIRST step of both jobs in deploy-demo-box.yml, and that
# position is the whole point: it is the only disk guard a job timeout cannot
# skip. The two prune steps at the end of the deploy job (`Prune dangling
# images`, `Prune build cache older than 24h`) both carry `if: always()`, which
# covers a failed step but NOT the runner being killed. When the deploy job
# trips its own `timeout-minutes` mid build, every remaining step including
# both prunes reports no conclusion at all and never runs. Run 33237021243 on
# 2026-08-29 did exactly that: it timed out inside `Rebuild + recreate changed
# services` with the box at 11 GB free, so the deploy that filled the disk was
# also the deploy whose cleanup never fired. Cleanup that only runs on the
# happy path is not a floor.
#
# It guards `migrate` as well as `deploy`, and the migrate case is the worse
# one. Postgres responds to ENOSPC by panicking and shutting the engine down to
# protect data integrity, so a migration that runs out of disk mid transaction
# takes down the stack that is currently serving, where a refused deploy merely
# leaves the previous stack running. Gating only the build would also let the
# schema advance while the code that matches it never ships.
#
# Failing before the work is deliberately preferable to failing halfway. This
# box holds the Postgres data directory, the object storage buckets and the
# chat data volume, so a filesystem that fills partway through a recreate does
# not merely abort a deploy, it stops the database accepting writes with
# containers already half torn down (the 2026-08-01 shape).
#
# Thresholds, measured on the box 2026-08-29. The built images total about
# 26 GB, of which open-webui alone is 7.4 GB and web-console-prod 2.4 GB, and a
# base image bump that invalidates every Dockerfile rebuilds all of them in one
# run. The floor has to sit ABOVE the known failure point, not below it: the
# box was at 11 GB free when run 33237021243 timed out mid recreate, so a 10 GB
# floor would have waved through the exact deploy this exists to stop. Hence
# 15. The 25 GB warn line is loud on every run long before it is fatal.
#
# Both are overridable from the environment, which is also the escape hatch. A
# hard floor with no override can wedge the box: below it nothing deploys,
# including a change meant to reduce what the box builds. Setting the floor
# from a `workflow_dispatch` input is deliberately a manual, audited action;
# it is NOT readable from a commit message, so no ordinary push can bypass it.
set -euo pipefail

fail_gb="${HIVE_DEPLOY_DISK_FLOOR_GB:-15}"
warn_gb="${HIVE_DEPLOY_DISK_WARN_GB:-25}"
target="${HIVE_DEPLOY_DISK_TARGET:-/var/lib/docker}"
# Ceiling on the build cache reclaim below, in seconds. Overridable for the
# same reason the thresholds are: 300 was measured against a 12.42 GB reclaim
# on this box, and a box that has accumulated much more than that would have
# its reclaim killed partway by a limit nobody can move without a release.
# Deleting cache is incremental, so a killed prune keeps whatever it freed, but
# the run would then be quietly less effective than it looks.
reclaim_timeout_s="${HIVE_DEPLOY_DISK_RECLAIM_TIMEOUT_S:-300}"

# All three are reachable from a human-typed workflow_dispatch input, and a bad
# value here does not fail, it silently stops gating. `[ 22 -lt bad ]` exits 2,
# which an `if` treats as false, so the floor check is skipped and the deploy
# proceeds with a stderr line nobody reads. A warn line below the floor is the
# same hole by a different route: the `>= warn` early exit fires first and
# returns 0 before the floor is ever compared. Refuse all of them, loudly.
for pair in \
  "HIVE_DEPLOY_DISK_FLOOR_GB=$fail_gb" \
  "HIVE_DEPLOY_DISK_WARN_GB=$warn_gb" \
  "HIVE_DEPLOY_DISK_RECLAIM_TIMEOUT_S=$reclaim_timeout_s"; do
  case "${pair#*=}" in
    "" | *[!0-9]*)
      echo "::error::${pair%%=*} must be a non-negative whole number, got '${pair#*=}'"
      exit 1
      ;;
  esac
done
if [ "$warn_gb" -lt "$fail_gb" ]; then
  echo "::error::HIVE_DEPLOY_DISK_WARN_GB ($warn_gb) is below HIVE_DEPLOY_DISK_FLOOR_GB ($fail_gb), which would skip the floor check entirely"
  exit 1
fi
# Zero is a valid argument to `timeout` and means no limit at all, which would
# let a wedged prune hold the job until its own timeout-minutes kills it, and
# that kill takes the disk check with it. Refuse it rather than accept a value
# that silently removes the bound this variable exists to set.
if [ "$reclaim_timeout_s" -eq 0 ]; then
  echo "::error::HIVE_DEPLOY_DISK_RECLAIM_TIMEOUT_S must be at least 1 second; 0 means no timeout, which would let a wedged reclaim run until the job is killed and take the disk check with it"
  exit 1
fi

# `target` is the docker image store rather than `/` so this stays correct if
# the store is ever moved to its own filesystem. Today they are one volume.
#
# df runs in its own command rather than inline in a pipeline: under
# `set -euo pipefail` a failing df would abort this script before the checks
# below run, so the job would go red with no annotation saying why, which is
# the least useful way for a disk guard to fail.
#
# A function because the guard reads free space twice, once before reclaiming
# build cache and once after, and the two readings have to be parsed and
# rejected identically. Sets $free_gb; returns non-zero after annotating.
read_free_gb() {
  local avail
  if ! avail=$(df -BG --output=avail "$target" 2>&1); then
    echo "::error::could not read free space on $target: $avail"
    return 1
  fi
  free_gb=$(printf '%s\n' "$avail" | tail -1 | tr -dc '0-9')
  if [ -z "$free_gb" ]; then
    echo "::error::could not parse a size out of df on $target: $avail"
    return 1
  fi
}

read_free_gb || exit 1

summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
echo "free space on $target: ${free_gb}G (warn <${warn_gb}G, fail <${fail_gb}G)"

if [ "$free_gb" -ge "$warn_gb" ]; then
  exit 0
fi

# ------------------------------------------------------------------
# Reclaim unused build cache, then measure again. Issue #1419.
#
# Nothing on this box prunes build cache in a way that reaches it. Every merge
# to main deploys and every deploy builds, and on 2026-08-29, after roughly 43
# merges, `docker system df` read: images 26.72GB with 1.391GB reclaimable,
# build cache 37.68GB with 12.42GB reclaimable. So the growth is cache, not
# images, and the gate started refusing deploys at 14G free against the 15G
# floor three runs in a row. `docker builder prune -f` recovered the whole
# 12.42GB and moved the box from 14G to 23G, touching no image, no volume and
# no rollback path.
#
# The deploy job does already end with `docker builder prune -f --filter
# until=24h`, and that filter is precisely why it reclaims nothing on the days
# that matter: after 43 builds in one day every cache record is younger than 24
# hours, so the filter excludes exactly the records consuming the disk. The
# unfiltered form is the one that was measured, and it is still the safe one:
# without `-a` BuildKit only releases records it reports as reclaimable, which
# by definition excludes anything backing an image that currently exists.
#
# Three properties keep this from laundering a real disk problem into a pass.
#
# It only runs below the warn line, so a healthy box is never pruned and its
# numbers are never massaged. It prints the reading from before the reclaim
# alongside the one after, so a box whose problem is not build cache shows a
# small delta and is still refused by the floor. And any run that needed a
# reclaim says so with an annotation, so a box that only deploys because of
# this step is loud on every deploy rather than quietly dependent on it.
#
# It lives inside the guard rather than in a preceding workflow step, and that
# is what makes "a failed reclaim cannot skip the check" structural instead of
# a promise. There is no second step whose failure or cancellation could stop
# the comparison from running, and no `if:` expression to get wrong. A prune
# that fails, or outlives its timeout, is reported and then ignored: the
# thresholds are applied to whatever `df` actually says, so this can only ever
# turn a refusal into a pass by genuinely freeing the space.
before_gb="$free_gb"
reclaim_note=""
echo "::group::docker builder prune -f"
if timeout "$reclaim_timeout_s" docker builder prune -f; then
  :
else
  reclaim_note=" The build cache reclaim failed or timed out, so this is the space that was already free."
  echo "::warning::docker builder prune failed or timed out on the demo box; continuing to the disk check against real free space. A failed reclaim never turns a refusal into a pass."
fi
echo "::endgroup::"

read_free_gb || exit 1
echo "reclaimed build cache: ${before_gb}G -> ${free_gb}G free on $target.$reclaim_note"

# One sentence, carried into every branch below and into the step summary, so
# whichever line a reader lands on has BOTH readings on it. Reporting only the
# result would hide how close the box was, which is the difference between
# "build cache accumulated again, as expected" and "something else is eating
# the disk and the reclaim barely moved it".
reclaimed="Unused build cache was already reclaimed: ${before_gb}G free before, ${free_gb}G after.$reclaim_note"

if [ "$free_gb" -ge "$warn_gb" ]; then
  # Above the warn line only because of the reclaim. Still loud: a box that
  # needs this every deploy is a box whose cache is growing faster than its
  # headroom, and that is worth seeing before the floor stops it.
  echo "::warning::demo box was at ${before_gb}G free on $target, below the ${warn_gb}G warn line, and reached ${free_gb}G by reclaiming unused build cache.$reclaim_note"
  {
    echo "### Demo box disk: ${free_gb}G free, above the ${warn_gb}G warn line after a reclaim"
    echo "Proceeding. Nothing but unused build cache was removed. $reclaimed"
  } >> "$summary"
  exit 0
fi

# Below one of the thresholds even after the reclaim: show where the space
# went, so whoever reads this does not have to ssh in to find out.
echo "::group::docker system df"
docker system df || true
echo "::endgroup::"

# Named explicitly because a full disk failure is exactly when someone reaches
# for the biggest hammer available, and two of the obvious hammers are how this
# box loses its rollback path or its only copy of the data.
#
# The `docker rmi` advice is not a style preference. A rebuild that moves a tag
# leaves the previous image pinned only by the running container's snapshot,
# and such an image does not appear in `docker images` at all. On 2026-08-29
# four of them were live on this box (markitdown, agent-console, supabase-db,
# proof-db). They are invisible to any inventory a human takes before pruning,
# so the only thing that reliably protects them is `docker rmi` WITHOUT
# --force, which refuses an image any container references. A prune, or an rmi
# with --force, has no such backstop.
hint="Reclaim safely by removing tagged images no container references and nothing in this repo names (superseded base images, old third-party versions); 'docker rmi' without --force refuses anything a container still holds. Do NOT run 'docker system prune -a' or 'docker image prune -a': both delete the previous stack images that are this box's only rollback path. Do NOT prune volumes: the database, the storage buckets and the chat data all live in them, with no off-box backup (issue #1000)."

if [ "$free_gb" -lt "$fail_gb" ]; then
  echo "::error::demo box has ${free_gb}G free on $target, below the ${fail_gb}G floor. Refusing to proceed. $reclaimed $hint"
  {
    echo "### Demo box disk: ${free_gb}G free, refused"
    echo "Below the ${fail_gb}G floor. $reclaimed $hint"
    echo ""
    echo "To override for one run after reclaiming, re-run this workflow via workflow_dispatch with a lower \`disk_floor_gb\`."
  } >> "$summary"
  exit 1
fi

echo "::warning::demo box has ${free_gb}G free on $target, below the ${warn_gb}G warn line. Proceeding. $reclaimed $hint"
{
  echo "### Demo box disk: ${free_gb}G free"
  echo "Below the ${warn_gb}G warn line. $reclaimed $hint"
} >> "$summary"
