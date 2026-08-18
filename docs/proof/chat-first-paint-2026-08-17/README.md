# Chat first paint, before and after

Measured against the live deployment at `https://chat-hive.scubed.co/`, signed
in through the audited one-time-token mint (`live-auth.mjs`), with a
`PerformanceObserver` armed in an init script before navigation. The recorded
milestone is the moment the composer (`#chat-input`) becomes visible, because
that, and not first paint, is the moment the product can be used.

## Method for the A/B

The deploy of this branch happens on merge, so both arms were measured before
merge by serving a locally built chat bundle in place of the deployed one while
leaving everything else alone: same origin, same backend, same tunnel, same
account, same machine, back to back. Only `/_app/immutable/*` and the SPA entry
document are served from the local build. Every API call, the OAuth callback and
all other static assets go to the live deployment untouched.

- **Before** arm: bundle built from `main` at `1dc67ee69`.
- **After** arm: bundle built from this branch.

Both bundles were rebuilt and remeasured after the session guard commit, so the numbers below describe the code this branch actually ships.

Two passes were taken. The headline is the paired one, because it is the only
shape that survives a shared machine whose load moved by a factor of three
during the work.

**Paired and interleaved, eight pairs, shipped bundle.** Each pair loads both
bundles back to back in the same browser process, alternating which arm goes
first.

| | Before | After |
|---|---|---|
| Composer visible, median | 5134 ms | 2510 ms |
| Median total blocking time | 1525 ms | 797 ms |
| Pairs won by the after arm | | 8 of 8 |
| Serial API round trips before the composer | 5 | 2 |

**Sequential, six loads per arm, calmer window, first revision of the branch.**

| | Before | After |
|---|---|---|
| Composer visible, median | 3115 ms | 2091 ms |
| Composer visible, range | 1872 to 3301 ms | 1783 to 2298 ms |

Both passes agree on direction and on rough magnitude. The paired window was
busy, load average 20 to 35 on a 24 core machine, which inflates both arms and
widens the gap, because a loaded client pays scheduling delay on every extra
serial step.

**Which bundle was which.** Each arm's build was invoked with its own
`APP_BUILD_HASH`, which Vite compiles into the bundle, and the value was read
back out of the built chunks afterwards: `perf-baseline` in the before tree and
`perf-after2` in the after tree. Neither arm can have come from another build.
Both were produced by `docker run node:22-alpine3.20 npm run build` against the
same worktree and the same `node_modules`, with only the three source files
differing; no shared local image tag is involved anywhere in this measurement.

The same measurement taken directly against the deployed bundle with no
interception at all, for reference: composer visible at a median of 2813 ms warm
(five loads) and 3182 ms cold (three loads). The interception harness adds a
little overhead, which is why the before arm reads slightly higher than the
uninstrumented deployment; both arms carry it equally.

## Files

- `timings.md` — every run, plus the API waterfall for one run of each arm.

The screenshots are posted on the pull request itself, through
`scripts/post-pr-visual-proof.sh`, rather than committed here: a release asset
survives the squash-and-delete of this branch, which a path in this directory
referenced from a rendered comment does not.
