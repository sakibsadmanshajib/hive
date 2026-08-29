# Chat upload cap: capture log, 2026-08-29

Pull request #1436, issue #1428. Follow-up to #1426 and #1405.

Environment: the deployed demo box, `https://chat-hive.scubed.co`, reached
through the demo-box SOCKS proxy. Chromium 1440x950, Playwright.

Identity: `owui-e2e+capdiv2908@hive-e2e.invalid`, a run-key-scoped fixture
account created for this run by `scripts/seed-owui-e2e-user.py` with
`OWUI_E2E_RUN_KEY=capdiv2908`. Not `demo@hive-demo.invalid`. The session was
minted through GoTrue's admin one-time-token flow, the same two calls
`apps/web-console/tests/e2e/support/live-auth.mjs` makes. No password was
read, set, reset or rotated at any point, on this account or any other.

No URL in this capture carries a credential in its query string. Every page
recorded below is `https://chat-hive.scubed.co/`, with no query string at
all, and the screenshot shows that bare origin.

## Before the change

Persisted config read straight out of the running container's
`/data/webui.db`:

    rag.file.max_size = 100
    rag.file.max_count = null
    rag.file.allowed_extensions = ["bash", "bat", ... 69 entries ...]

Container environment, read with `printenv`:

    hive-open-webui-1  RAG_FILE_MAX_SIZE=100
    hive-edge-api-1    RAG_MAX_UPLOAD_BYTES=26214400
    hive-markitdown-1  MAX_UPLOAD_BYTES=26214400

`/home/sakib/hive/.env` line 98 on the box: `RAG_FILE_MAX_SIZE=100`. That
explicit value is what defeated the `${RAG_FILE_MAX_SIZE:-25}` compose default
PR #1426 added.

`GET /api/config`, both auth states, same deployment, same minute:

    unauthenticated  keys: default_locale, features, name, oauth, status, version
                     file block: ABSENT
    authenticated    file: {"max_size": 100, "max_count": null,
                            "image_compression": {"width": null, "height": null}}

The block is auth-gated. Open WebUI's token lives in localStorage rather than
a cookie, so a fetch without an explicit Authorization header sees the
unauthenticated shape.

Composer, real attachments through the file input:

    small-64kb.txt    65536 bytes       POST /api/v1/files/?process=true 200,
                                        second request 200, chip resolves
    window-30mb.txt   31457280 bytes    POST issued, NO response in 43805 ms,
                                        spinner, no progress, no timeout, no error
    over-110mb.txt    115343360 bytes   refused client side in under 1 s,
                                        toast "File size should not exceed 100 MB.",
                                        ZERO requests issued

The last line is the client-side guard working. It is not dead code, and the
only thing wrong with it was the number it was given.

Server behaviour, same file, no browser:

    inside the box's docker network, straight at open-webui:8080
      30 MB   200 in 0.334 s, accepted and stored
      110 MB  413 in 14.234 s

    from the box, out to https://chat-hive.scubed.co and back through Cloudflare
      24 MB   200 in 118.893 s at 212 KB/s
      25 MB   524 in 125.051 s
      30 MB   524 in 125.129 s

    from a WSL client through the demo-box SOCKS proxy
      30 MB   524 in 125.306 s at 251 KB/s

The backend was never hanging. The 125 second figure is constant and size
independent, which is a timeout rather than a bandwidth limit, and it is
Cloudflare's origin timeout. Filed separately as issue #1435, because the
transport wall sits in front of the developer API's /v1/rag upload too and is
not something a size cap can fix.

## After the change

    window-30mb.txt   31457280 bytes    refused immediately, watched for 13123 ms,
                                        toast "File size should not exceed 25 MB.",
                                        ZERO requests issued

Screenshot: posted to the pull request through
`scripts/post-pr-visual-proof.sh`, which uploads to the permanent
`visual-proof-assets` release rather than to a branch that merge deletes.

HONEST SCOPE OF THIS CAPTURE. The frontend, the account, the session and the
file are all real and deployed. The one value stubbed is `file.max_size` in
the `/api/config` response, rewritten from the 100 the box publishes today to
the 25 that `derived_upload_cap` produces from `RAG_MAX_UPLOAD_BYTES`. That is
the single value this pull request changes, and stubbing it is the only way to
exercise the deployed composer's post-change behaviour before the deploy that
sets it, since the deploy happens on merge. It is not an end-to-end capture and
must not be read as one. That the derivation produces 25 from 26214400 is
proven by `scripts/test_owui_rag_env_config.py`, not by this screenshot. A
genuine end-to-end capture should be taken against the deployed result once the
merge lands, by re-running the same probe with the stub removed.

## Reproducing

    scripts/seed-owui-e2e-user.py with OWUI_E2E_RUN_KEY set, run inside a
    container on the box's hive_default network, since SUPABASE_URL names an
    internal hostname the dev box cannot resolve.

    Mint through GoTrue's admin one-time-token flow, then drive Chromium with
    proxy: { server: "socks5://127.0.0.1:1080" }. The chat surface needs the
    OIDC hop's "Approve" button clicked once per fresh session.
