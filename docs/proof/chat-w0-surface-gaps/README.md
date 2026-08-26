# Proof: five chat-surface gaps fixed and verified against a running build

Text log only here. Images are posted as permanent GitHub Release assets via
`scripts/post-pr-visual-proof.sh`, because a `raw.githubusercontent.com` link
pinned to this branch would 404 the moment the branch is deleted at
squash-merge. `npm run lint:proof-tokens` scans this directory and nothing
else, which is why the log is committed here.

## Substrate

`hive-open-webui:w0chatfix`, built locally from this branch with
`docker build -f deploy/docker/Dockerfile.open-webui .`. The build ran the
same `npm run test:frontend -- --run` gate and bundle-content assertions the
real image build runs (`hive: shell present, removed surfaces absent`), then
was run as a standalone container:

- `ENABLE_LOGIN_FORM=true`, `ENABLE_SIGNUP=true`: local email/password signup,
  the code path the fix for item 1 specifically has to cover (the backend
  OAuth-signup patch, `hive_display_name.py`, never runs on this path).
- `RAG_EMBEDDING_ENGINE=openai` pointed at a tiny stdlib-only Python stub
  standing in for control-plane, so the boot does not try to download a local
  embedding model. The same stub answers `POST /internal/chat/credits/balance`
  with `{"available_credits": 9789478244, "usage_today_credits": 395640}`,
  the exact figures from the original bug report, so the real
  `hive_credits.py` proxy chain (Open WebUI backend to `HIVE_CONTROL_PLANE_URL`)
  is exercised end to end rather than mocked at the frontend.
- No live LLM configured (`ENABLE_OPENAI_API=false`, `ENABLE_OLLAMA_API=false`).
  Items 3 and 4 need a rendered code block and a rendered user turn, not a
  real model response, so the transcript was seeded with
  `POST /api/v1/chats/import` (an authenticated, already-shipped Open WebUI
  endpoint) rather than routed through a provider.

## What is shown

`01-greeting-and-credits-banner.png` (posted as
`02-after-item1-item2.png` locally) — home screen after signing up with the
`name` field set to the literal string `qa-tester@hive.test` (the exact shape
of the regression: a `name` column already holding the raw email address).
Greeting reads "Good morning, Qa Tester", never the email. Banner reads
"You've used $0.000396 today · $9.79 remaining", not a raw ten-digit integer.

`02-transcript-codeblock-and-user-turn.png` (posted as
`03-item3-item4-transcript.png` locally) — the imported conversation. The
code block's action row shows only `Collapse` and `Copy`; no `Run`, no `Save`.
The user turn ("In one sentence, what is a mortgage?") is plain right-aligned
text with no filled pill background.

Measured live in the DOM after this PR's changes, against the running
container described above:

| Measurement | After this PR (measured live) |
|---|---|
| `button.run-code-button` count | 0 |
| `button.save-code-button` count | 0 |
| Greeting text | "Good morning, Qa Tester" |
| Credits banner text | "You've used $0.000396 today · $9.79 remaining" |

The "before" values (`run-code-button` present, `save-code-button` present,
greeting reading the raw email, banner reading a raw ten-digit integer) are
not independently re-measured against a second, unpatched build in this run;
they are the documented, unfixed behavior of the code this PR changes and the
literal `chat-02-empty.png` / `chat-05-conversation.png` captures taken the
same day (2026-08-26, 03:43-03:45 UTC) against the deployed demo box, cited
in the PR body. Building and running a second, unmodified container purely to
re-confirm the "before" state was judged not worth the added build time here.

`save-code-button` deserves a callout on its own: the first pass of this fix
flipped only `CodeBlock.svelte`'s own `save` default, and a build against
that first pass still showed the button, live, because `ResponseMessage.svelte`
was passing `save={!readOnly}` for the live message tree, overriding that
default on every real assistant turn. Caught by this verification run itself,
not by the plan; fixed by pinning that call site to `save={false}` explicitly
(see the PR body's Buglog entry), then re-verified live at count 0 in the
table above.

## What is deliberately not claimed

Item 5 (Cowork step list filtering) is verified by the automated test suite
(`coworkMode.test.ts`, both branches of `describeEvent` that used to return
the literal apology string now pinned to return `null`), not by a live
screenshot of a Cowork run. Reproducing a real run with a malformed or unknown
event type needs the full agent-engine sandbox plus an OAuth-minted session
plus edge-api and control-plane, which is out of reach for local verification
at this scope. Said so explicitly rather than skip it silently or fabricate a
screenshot.

## Credential handling

No OAuth flow was exercised (local password signup only), so no URL in this
capture carries a token, code, or session credential in a query string or
fragment. The account (`qa-tester@hive.test` / the QA fixture password from
`.env`) exists only inside this throwaway local SQLite-backed container,
destroyed with it; no password was set, read, reset, or rotated on any shared
or deployed account.

## Re-verification after rebase onto main (2026-08-26, 27a4b31c5)

Between this PR's original verification pass above and merge, a peer session
landed two PRs on adjacent surface: #1233 ("one placeholder on the chat home,
asserted in the bundle") deleted the dead `ChatPlaceholder.svelte` duplicate
and reworked `Messages.svelte`'s empty-history branch; #1232 applied the same
`formatUsdFromCredits`/`formatCachePrice` invariant this PR ports to a
different surface (`apps/web-console`, untouched by this PR). Neither PR
touches a file this branch changes (`git diff --stat` against both confirms
zero overlap), and the merge of `origin/main` into this branch was clean, no
conflicts.

All five original bugs were re-confirmed present on `origin/main` at
27a4b31c5 (before re-applying this branch's fixes) by grepping the exact
code shapes the fixes target: `CodeBlock.svelte`'s `run` default,
`ResponseMessage.svelte`'s `save={!readOnly}` override, `UserMessage.svelte`'s
filled-pill class, `coworkMode.ts`'s two apology-string branches, and
`stores/index.ts`'s unwrapped `user` writable. None had been independently
fixed by the peer PRs; all five still applied cleanly.

Two CodeRabbit findings posted against this PR (both real, both fixed, both
threads resolved):

- `CodeBlock.svelte`: `CodeEditor`'s `onSave` callback was wired to
  `saveCode()` unconditionally, so `Ctrl/Cmd+S` inside the editor could still
  save a code block whose toolbar Save button was hidden (`save={false}`).
  Gated the callback on `save` itself.
- `displayName.ts`: `resolveDisplayName` sanitized an email-derived name but
  not an accepted server-provided one, so a bidirectional-override character
  in an upstream `name` field could reach the rendered display name
  unfiltered. Now sanitizes both paths; regression test added.

### Fresh live verification (post-rebase, post-fix)

Same method as the original pass: `hive-open-webui:pr1223-verify`, built
locally from this branch's current HEAD (`docker build -f
deploy/docker/Dockerfile.open-webui .`), run standalone with
`ENABLE_LOGIN_FORM=true` local password signup and the same stdlib credits
stub, against the exact bug-report figures.

```
GET /auth -> http://localhost:23004/auth
mode: signup (onboarding)
after sign-in/signup -> http://localhost:23004/
greeting text: Good morning, Qa Tester
credits banner text: You've used $0.000396 today · $9.79 remaining
import chat -> 200 [...]
imported chat id: 9dc04018-52e4-4b7c-bbf6-c861ccc88c3f
run-code-button count: 0
save-code-button count: 0
```

Two screenshots posted via `scripts/post-pr-visual-proof.sh`:

- Chat home: greeting "Good morning, Qa Tester" (never the raw
  `qa-tester@hive.test` used as the signup name, the literal regression
  shape), quick-start chips render (Code/Write/Explain/Analyze), confirming
  #1233's single-`Placeholder.svelte` mount and this branch's `displayName`
  normalization compose without collision.
- Imported transcript: code block action row shows only Collapse/Copy (no
  Run, no Save), user turn right-aligned with no filled background, sidebar
  display name also reads "Qa Tester" (the wrapped `user` store normalizes
  every consumer, not just the greeting).

Item 5 (Cowork step list filtering) remains verified by the automated test
suite only, unchanged from the original pass: `coworkMode.test.ts` 47 tests
green, both `describeEvent` branches that used to return the apology string
now pinned to `null`. Same reasoning as above: a live Cowork run needs the
full agent-engine sandbox, out of reach for local verification.

Full unit suite after the rebase and both CodeRabbit fixes: 198/198 (15 in
`displayName.test.ts`, including a new sanitize-bidi-character regression
test), all 13 `lib/hive/*.svelte` components compile clean.

No OAuth flow was exercised in this re-verification either; the account
lived only in the throwaway container's SQLite database, destroyed with it.
