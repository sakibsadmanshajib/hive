# Voice proof, issue #1562 and issue #1381

Captured in Chromium against a running Hive stack on 2026-08-30: throwaway
Postgres on the real migration chain, the real LiteLLM, real Groq, the forked
chat front end behind `caddy-owui`. The browser only ever talked to
`http://localhost:24503`, which is the Caddy front, and the capture asserts
that rather than assuming it.

Three captures, one flow, three builds.

| File | Build | What it shows |
| --- | --- | --- |
| `read-aloud-before-fix.png` | `origin/main` | Read Aloud fails, and the toast publishes `url='http://edge-api:8080/v1/audio/speech'` to the signed-in user |
| `read-aloud-error-redacted.png` | chat image patched, edge-api still `origin/main` | The same failure, now `url='[redacted]'`. The diagnostics survive, the internal address does not |
| `read-aloud-playing.png` | both fixes | Read Aloud succeeds: 200, 15385 bytes, and the browser's own `<audio>` element reports the clip decoded |

## What the first image settles

Issue #1562 reports that voice mode "calls edgeapi:8080/v1". It does not. The
chat front end is same-origin throughout, and the guard test
`apps/web-console/tests/unit/browser-origin-hosts.test.ts` now holds that.

What actually happened is in the first image: Open WebUI's audio router
stringified the aiohttp exception into the error it returned, aiohttp bakes the
request URL into that string, and the browser rendered it. The user was shown
the internal address, not sent to it. The 500 underneath is issue #1381,
`response_format`, and it is fixed in the same pull request.

## Reproducing

The captures are not wired into a workflow: the stack they need is a local
bring-up (see the pull request body for the recipe). The regression cover that
does run in CI is the unit and script suites named in the pull request, plus
the SDK conformance suite in `ci.yml`'s `live-integration` job, whose two audio
expected-failure markers this change removes.
