# Chat-first login coherence, captured 2026-08-06

Proof for the three login findings raised by the live adversarial review of
`chat-hive.scubed.co`. Captured against locally running stacks, because this
branch cannot deploy to the demo box.

No real credential appears in any capture. The `authorization_id` in image 04
is the literal string `PLACEHOLDER-NOT-A-REAL-ID`, typed by the capture script;
no authorization request was ever created. The Open WebUI proof container was
a throwaway with placeholder OAuth client values and its own scratch volume,
destroyed after capture.

## 1. The dead login form on chat

| | |
|---|---|
| `01-owui-login-before.png` | `ENABLE_LOGIN_FORM` unset. Email, Password and a **Sign in** button render above **Continue with Hive**. This is the live bug: every account on this deployment is SSO-only, so that form answers correct Hive credentials with HTTP 400 "The email or password provided is incorrect". |
| `02-owui-login-after.png` | `ENABLE_LOGIN_FORM=false`, **same container image and same Docker volume**, restarted. Only **Continue with Hive** remains. |

Both frames come from `hive-open-webui:v0.10.2-branded` built from this branch.
The after-state container was restarted onto the volume the before-state
container had already seeded, which is the case that matters: Open WebUI's
`Config.seed_defaults` writes `ui.enable_login_form` on first boot and the
database outranks the environment from then on, so the compose variable alone
would have been a silent no-op on the demo box. Its startup log shows the
reconcile winning:

```
INFO | open_webui.config:seed_registered_defaults:52 - hive: reconciled Open WebUI
RAG config from env: rag.embedding_engine=openai,
rag.embedding_model=sentence-transformers/all-MiniLM-L6-v2, ui.enable_login_form=False
```

and `/api/config` flips accordingly:

```
before: "enable_login_form":true
after:  "enable_login_form":false
```

Known cosmetic residue in `02`: the horizontal rule that belonged to the "or"
divider still renders, without the word "or". That is upstream's own markup
gating and it is not worth forking a pinned image over.

## 2. Sign-in copy follows the journey

| | |
|---|---|
| `03-console-direct-visit.png` | `/auth/sign-in` with no `next`. Unchanged: "Sign in to your console", "Manage API keys, credits, and usage analytics for your workspace." |
| `04-console-from-chat-consent.png` | `/auth/sign-in?next=/oauth/consent?authorization_id=...`, the path chat's OIDC round-trip uses. Now "Sign in to Hive", "Use your Hive account to continue." No mention of console, API keys, credits or analytics. |

Captured against `next dev` from this branch, which is why the Next.js dev
badge is visible in the lower-left corner of both frames.

## 3. Submit progress state

No change shipped, and none needed. `handleSubmit` sets `loading` before
awaiting `signInWithPassword` and clears it only on the error path; the success
path calls `navigate()`, a hard `window.location.assign`, so the button stays
disabled and reading "Signing in…" until the browser leaves the document. The
round trip is already covered end to end. The remaining wait the review
measured is on the pages that come after this one, not on this button.

## Reproducing

```bash
cd deploy/docker && docker compose build open-webui
# before: run the image with ENABLE_LOGIN_FORM unset, create the first admin
# after:  restart the same container onto the same volume with ENABLE_LOGIN_FORM=false
curl -s http://localhost:3099/api/config    # watch features.enable_login_form flip
```
