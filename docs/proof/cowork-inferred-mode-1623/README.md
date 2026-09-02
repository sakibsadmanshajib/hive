# Visual proof: one composer, inferred kind of task (issue #1623, PR #1729)

## What was captured, and against what

The Hive chat image built from this pull request's branch
(`deploy/docker/Dockerfile.open-webui`, tag `hive-owui:pr1623`), run standalone
with `docker run`. That is the proof harness `Dockerfile.open-webui` documents
for itself. The frontend bundle under test is the real one the deploy ships,
built by the same `npm run build` the deploy runs.

## What is real here and what is stubbed, stated plainly

Real: the whole front end. The container, the built bundle, the composer, the
Cowork row, the submit path, the request body the browser puts on the wire, the
transcript turn, the progress line it renders, and the correction control.

Stubbed: two responses. The model list, because `submitHandler` in
`Chat.svelte` refuses a submission before it reaches the Cowork branch when no
model is selected and a standalone container has no provider configured. And
the answer to `POST /api/v1/hive/agent/tasks`, because there is no edge-api
behind this container.

**The stub decides nothing this proof claims.** For each of the two instruction
strings, the `pack` it answers with is the value
`apps/control-plane/internal/agenttask/infer.go` returns for that exact string,
and both strings are pinned in `infer_test.go`, which runs in CI on every push.
The Go run over those same two strings is recorded at the bottom of
`capture.log`. So the frames show the front end rendering a decision the Go
inference is mechanically held to, rather than a value a capture script chose
for itself.

The half that is entirely unstubbed is also the half that is the regression
evidence: the request body the browser built carries no `pack` key at all, in
both submissions. That is recorded verbatim in `capture.log`.

Not captured here, and deliberately not faked: an `agent_tasks` row whose pack
was resolved by the deployed control-plane end to end. That after-state does
not exist until this merges and `deploy-demo-box` runs, so it is captured after
the deploy rather than substituted with a stale or unrelated frame.

## The measurement that matters

Before this change the composer carried two segmented controls in Cowork mode,
and a query for clickable elements matching "Knowledge work" or "Coding"
returned 1 each (that is the measurement PR #1518 recorded for issue #1500).
Against this build both return 0, `[data-hive-pack]` returns 0, and the
composer's whole control surface in Cowork mode is the two segment
`Chat | Work` toggle. The two submissions then land on different packs with
nothing chosen: "Hive ran this as a coding task." for the `server.go` request
and "Hive ran this as a knowledge work task." for the one page brief.

## Credentials

None. No URL in this capture carries a credential in a query string or
fragment, so there is nothing to redact in either the log text or the
screenshot pixels. The throwaway local account exists only inside a container
with an empty SQLite database that is discarded after the run; its password is
generated per run inside the capture script, is never written to this log, and
the signup form is never screenshotted. No shared or fixture account was
touched and no password anywhere was set, reset or rotated.

## How to repeat it

```
$ docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui:pr1623 .
$ docker run -d --name proof1623-owui -p 127.0.0.1:3623:8080 \
    -e WEBUI_SECRET_KEY=proof1623-local-secret -e ENABLE_SIGNUP=true \
    -e ENABLE_LOGIN_FORM=true -e OAUTH_AUTO_REDIRECT=false \
    hive-owui:pr1623
$ docker run --rm --network container:proof1623-owui \
    -v "$PWD/docs/proof/cowork-inferred-mode-1623:/work" \
    -v "$PWD/docs/proof/cowork-inferred-mode-1623/out:/out" -w /work \
    -e OWUI_URL=http://localhost:8080 -e PROOF_PASSWORD="$(openssl rand -hex 12)" \
    mcr.microsoft.com/playwright:v1.55.0-noble \
    sh -c 'npm i --silent playwright@1.55.0 && node capture.mjs'
```

## Files

- `capture.mjs` — the capture script, including the stub and what it does not decide.
- `capture.log` — the full transcript of the run, including both request bodies.

The screenshots are attached to PR #1729 as inline images through
`scripts/post-pr-visual-proof.sh`, which uploads them to the permanent
`visual-proof-assets` release rather than to a branch that merge will delete.
