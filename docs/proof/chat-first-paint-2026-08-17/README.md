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

Three passes were taken, on two builds and in two very different machine load
conditions. The headline is the last one: paired, on the bundle this branch
actually ships, in a calm window. The other two are kept because they are
independent windows that agree, and because a disagreement would have been the
interesting outcome.

**Headline. Paired and interleaved, six pairs, final bundle** (`APP_BUILD_HASH`
`perf-final`), system load average about 10 on a 24 core machine. Each pair
loads both bundles back to back in the same browser process, alternating which
arm goes first.

| | Before | After |
|---|---|---|
| Composer visible, upper median | 3097 ms | 1916 ms |
| Composer visible, conventional median | 3034.5 ms | 1902.5 ms |
| Composer visible, range | 2518 to 3611 ms | 1576 to 2138 ms |
| Median total blocking time | 602 ms | 527 ms |
| Pairs won by the after arm | | 6 of 6 |
| Serial API round trips before the composer | 5 | 2 |

**Supporting, paired, eight pairs, post-guard bundle, busy window** (load
average 20 to 35): 5134 ms against 2510 ms upper median, after arm winning all
eight pairs, median total blocking time 1525 ms against 797 ms.

**Supporting, sequential, six loads per arm, pre-guard bundle** (the first
revision of this branch, before the session-tagging commit): 3115 ms against
2091 ms upper median.

All three agree on direction and on rough magnitude. The gap widens under load
for a reason that is a property rather than an artifact: a loaded client pays
scheduling delay on every extra serial step, so chain depth hurts most exactly
when the machine is least able to absorb it.

Medians are stated as upper medians throughout, since every sample count is
even, with the conventional median given alongside on the headline. Every raw
sample is in `timings.md`.

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
