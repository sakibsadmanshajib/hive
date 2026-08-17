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

Six loads per arm.

| | Before | After |
|---|---|---|
| Composer visible, median | 3115 ms | 2091 ms |
| Composer visible, range | 1872 to 3301 ms | 1783 to 2298 ms |
| Serial API round trips before the composer | 5 | 2 |

A 1024 ms improvement at the median, about a third of the wait, and a much
tighter spread.

The same measurement taken directly against the deployed bundle with no
interception at all, for reference: composer visible at a median of 2813 ms warm
(five loads) and 3182 ms cold (three loads). The interception harness adds a
little overhead, which is why the before arm reads slightly higher than the
uninstrumented deployment; both arms carry it equally.

## Files

- `timings.md` — every run, plus the API waterfall for one run of each arm.
- `after-composer-rendered.png` — the patched bundle against the live backend,
  composer rendered with the model resolved (`hive-auto`), which is what proves
  the earlier model fetch did not break model selection.
- `before-deployed-composer-rendered.png` — the deployed bundle for comparison.
