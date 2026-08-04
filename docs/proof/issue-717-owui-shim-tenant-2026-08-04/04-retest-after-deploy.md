# Retest after deploy run 30949975878 (sha f8321f30)

The mapping landed at 2026-08-04 20:42:13Z. PR #716 then merged, `deploy-demo-box.yml`
run **30949975878** completed with conclusion success and recreated the changed
services, so Open WebUI restarted and re-fetched its model list from edge-api
*after* the mapping existed. This file records the retest.

## edge-api is serving the shim account: 200 with six models

One clearly-marked diagnostic key was minted on the shim account itself
(`feb1e8e2…b598`), so it resolves the same account and therefore the same tenant
as the box's `…6_gDjQ` key. It is left in place deliberately for cleanup:

- account: `hive-demo-owui-shim` (`feb1e8e2-e291-4c05-9ec3-7817c891b598`)
- key id: `f4ceabaf-6f49-4523-9246-2376028d0809`
- nickname: `hive-717-diagnostic-2026-08-04-delete-me`
- masked suffix: `…onfflA`

```
GET https://api-hive.scubed.co/v1/models
Authorization: Bearer hk_…onfflA
-> HTTP 200
model count: 6
ids: hive-auto, hive-default, hive-embedding-default, hive-fast, hive-stt, hive-tts
```

```
POST https://api-hive.scubed.co/v1/embeddings
Authorization: Bearer hk_…onfflA
{"model":"hive-embedding-default","input":"issue 717 embedding probe"}
-> HTTP 429
{"error":{"message":"You exceeded your current quota, please check your plan and
billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}
```

The 403 `account_not_provisioned` is gone. The mapping is doing its job.

The 429 is a second, separate problem: the shim account has **zero** credit
ledger entries, so every metered call it makes now fails on quota instead of
provisioning. Model listing is unmetered and unaffected, but Open WebUI's
document-RAG embeddings and text-to-speech both run on this account and both
need a balance.

## Attribution of the still-empty picker: Open WebUI's own filter

- **(a) edge-api still refusing — ruled out.** 200 with six models above, on a key
  belonging to the same account.
- **(c) Open WebUI never re-fetching — ruled out.** Run 30949975878 recreated the
  service after the mapping existed, and Open WebUI's per-connection probe
  `GET /openai/models/0` answers **HTTP 200** with `{"data":[],"object":"list"}`.
  That route raises a non-200 when its upstream fetch fails, so the fetch
  succeeded and Open WebUI emptied the list itself.
- **(b) Open WebUI serving but filtering — confirmed, from our own config.**
  `deploy/docker/docker-compose.yml:662` sets `ENABLE_MODEL_FILTER: "true"` and
  `BYPASS_MODEL_ACCESS_CONTROL` is not set anywhere, so a session whose Open WebUI
  role is `user` only sees models present in Open WebUI's own Models registry.
  This box's registry is empty, so a non-admin sees zero no matter what edge-api
  returned. The only Open WebUI account with a known password here,
  `qa-tester@hive.test`, is tenant MEMBER, which `owui_role` maps to `user`.

A tenant OWNER maps to `owui_role` ADMIN, and the filter does not apply to an
admin session, so the picker is expected to populate for the demo users. That is
inference from config, not something this retest could observe, because no OWNER
password is available here and `ENABLE_ADMIN_PANEL` is `"false"`.

## Screenshot

`03-after-deploy-30949975878-picker-still-empty-nonadmin.png`, captured against
the running box after the deploy, signed in as the non-admin fixture. It shows
"No results found" and the banner records the host, the UTC capture time, and
`count=0` measured in the same session. It documents the non-admin view, not the
demo user's view.
