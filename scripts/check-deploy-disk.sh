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

# `target` is the docker image store rather than `/` so this stays correct if
# the store is ever moved to its own filesystem. Today they are one volume.
#
# df runs in its own command rather than inline in a pipeline: under
# `set -euo pipefail` a failing df would abort this script before the checks
# below run, so the job would go red with no annotation saying why, which is
# the least useful way for a disk guard to fail.
if ! avail=$(df -BG --output=avail "$target" 2>&1); then
  echo "::error::could not read free space on $target: $avail"
  exit 1
fi
free_gb=$(printf '%s\n' "$avail" | tail -1 | tr -dc '0-9')
if [ -z "$free_gb" ]; then
  echo "::error::could not parse a size out of df on $target: $avail"
  exit 1
fi

summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
echo "free space on $target: ${free_gb}G (warn <${warn_gb}G, fail <${fail_gb}G)"

if [ "$free_gb" -ge "$warn_gb" ]; then
  exit 0
fi

# Below one of the thresholds: show where the space went, so whoever reads this
# does not have to ssh in to find out.
echo "::group::docker system df"
docker system df || true
echo "::endgroup::"

# Named explicitly because a full disk failure is exactly when someone reaches
# for the biggest hammer available, and two of the obvious hammers are how this
# box loses its rollback path or its only copy of the data.
hint="Reclaim safely by removing tagged images no container references and nothing in this repo names (superseded base images, old third-party versions); 'docker rmi' without --force refuses anything a container still holds. Do NOT run 'docker system prune -a' or 'docker image prune -a': both delete the previous stack images that are this box's only rollback path. Do NOT prune volumes: the database, the storage buckets and the chat data all live in them, with no off-box backup (issue #1000)."

if [ "$free_gb" -lt "$fail_gb" ]; then
  echo "::error::demo box has ${free_gb}G free on $target, below the ${fail_gb}G floor. Refusing to proceed. $hint"
  {
    echo "### Demo box disk: ${free_gb}G free, refused"
    echo "Below the ${fail_gb}G floor. $hint"
    echo ""
    echo "To override for one run after reclaiming, re-run this workflow via workflow_dispatch with a lower \`disk_floor_gb\`."
  } >> "$summary"
  exit 1
fi

echo "::warning::demo box has ${free_gb}G free on $target, below the ${warn_gb}G warn line. Proceeding. $hint"
{
  echo "### Demo box disk: ${free_gb}G free"
  echo "Below the ${warn_gb}G warn line. $hint"
} >> "$summary"
