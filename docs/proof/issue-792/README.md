# Issue #792 evidence

Everything here was captured against a booted Open WebUI, not against a unit
test. #776 passed its tests, merged, deployed, and changed nothing, so the bar
for this fix is a running stack.

## Harness

An isolated stack, deliberately not the shared one: the pinned Open WebUI image
plus a pgvector container and a stub gateway that serves exactly the six Hive
catalog aliases on `/v1/models` and answers the three upstream calls Open WebUI
makes (chat completions, RAG embeddings, `audio/speech` and
`audio/transcriptions`). Open WebUI's environment mirrors the `open-webui`
service in `deploy/docker/docker-compose.yml`, including
`BYPASS_MODEL_ACCESS_CONTROL: "true"`.

Two personas, because the demo box promotes every tenant owner to an Open WebUI
administrator (#748) and Open WebUI's access control treats the two completely
differently:

* `owner@harness.local` — Open WebUI role `admin`, the persona an actual demo
  user has.
* `member@harness.local` — Open WebUI role `user`.

Audio is wired to the gateway in the harness even though `docker-compose.yml`
does not set `AUDIO_*` today, because #792 names text-to-speech as a risk and a
risk that is not configured cannot be measured.

## Screenshots

| File | What it shows |
| --- | --- |
| `01-before-picker-owner.png` | Pre-fix picker, signed in as the owner persona: all six aliases, including `hive-embedding-default`, `hive-stt` and `hive-tts`. This is #792. |
| `02-after-picker-owner.png` | Same persona, same harness, patched image: three chat aliases. |
| `03-after-picker-listing-endpoint.png` | `/api/models`, the listing the picker reads, post-fix. |
| `04-after-gateway-list-unfiltered.png` | `/openai/models` in the same signed-in session post-fix: the gateway list still carries all six. Both halves of the requirement, from one running system, one moment. |

## Probes

`probe-*.json` are full runs across both personas: picker listing, gateway
listing, chat completion, document RAG upload plus ingest plus query, TTS and
STT.

| File | Scenario | Result |
| --- | --- | --- |
| `probe-a-before-fix.json` | #776 merged, bypass on, i.e. the deployed box | Both personas see all six. Everything else works. The fix is inert. |
| `probe-b-bypass-removed.json` | Bypass removed, no other change | Admin still sees all six and chats fine. Member sees an **empty picker** and gets **HTTP 400 "Model not found"** on chat. RAG ingest, RAG query, TTS and STT are **unaffected for both personas**. |
| `probe-c-after-fix.json` | The fix, bypass left on as deployed | Both personas see three chat aliases; gateway list still six; chat, RAG, TTS and STT all still work. |

Probe B is the measurement that decided the design. The risks #792 named, RAG
and text-to-speech, do not depend on the bypass at all: neither consults Open
WebUI's model registry. What the bypass actually protects is chat itself for
non-admin members. And removing it would not even have hidden the three aliases
from the people who use the demo, because they are all administrators.

## Guard, red then green

`scripts/test_owui_model_picker_filter.py` run against the same harness, the
only difference being which image the container runs:

Pre-fix image, bypass on (the deployed world):

```
OWUI chat model picker filter regression (1 failure(s)):
  run_live: the chat picker still lists ['hive-embedding-default', 'hive-stt', 'hive-tts'] (issue #792). The picker filter is not on the response path, or something is gating it.
exit=1
```

Patched image, bypass still on:

```
live: gateway serves 6 models including all 3 non-chat aliases; picker lists 3 and none of them
OWUI chat model picker filter: 10 checks passed
exit=0
```

The static half, which is what CI runs, goes red the same way with the
Dockerfile's `RUN` line for the patch neutered and compose left as it is:

```
OWUI chat model picker filter regression (2 failure(s)):
  test_bypass_and_picker_filter_stay_coupled: docker-compose.yml sets BYPASS_MODEL_ACCESS_CONTROL: "true", which disables Open WebUI's per-model access control for every role, so an access_control-shaped fix cannot hide the non-chat aliases from the picker (this is #792). A picker filter that does not depend on access control must be present and must run in the image.
  test_dockerfile_stages_and_runs_the_patch: Dockerfile.open-webui no longer RUNs apply_model_picker_patch.py, so the three non-chat aliases are back in the picker
exit=1
```

That second failure is the one #776 could not have produced: it asserts the
mechanism reaches the image and is not the access-control mechanism the
deployment disables, rather than asserting that a value was written.
