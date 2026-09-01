# Chat surface trio: composer, left nav, mobile Enter (issues #1626, #1625, #1619)

Captured 2026-09-01 against a chat container built from this branch
(`docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui:proof-1626 .`),
running on a throwaway local network with a throwaway account and a stub
OpenAI-compatible gateway in place of `edge-api`. No provider key, no live
credential, nothing to redact in either the log or the pixels: the only secret
shaped value in the run is a local container password, and it is neither
printed here nor rendered on any frame.

The stub serves a model roster shaped like the live catalogue,
`deepseek-v4-flash, deepseek-v4-pro, hive-default, hive-fast, hive-free`, so
the alphabetical first entry that used to become the default is visible in the
same picker as the alias that now does.

## What the run reported

```
signed in as proof@hive-proof.invalid (password redacted; local throwaway container only)
GET /api/config as a signed-in user -> default_models = "hive-default"
#1626 composer: "Add Model" buttons = 0, "Remove Model" buttons = 0
#1626 composer model chip on a brand new conversation reads "hive-default"
#1626 the stub catalogue is deepseek-v4-flash, deepseek-v4-pro, hive-default, hive-fast, hive-free, so the alphabetical fallback this replaces would read deepseek-v4-flash
#1626 with ?models=hive-fast,hive-free in the URL: selector chips rendered = 1, chip reads "hive-fast"
#1619 mobile context reports {"ontouchstart":true,"maxTouchPoints":1,"innerWidth":390}, which is exactly the pair the old gate tested
#1619 user messages on screen after pressing Enter at 390x844 = 1
#1625 sidebar conversation rows after a cold reload = 6
#1625 chat list requests observed during that reload:
    GET /api/v1/chats/all/tags
    GET /api/v1/chats/pinned
    GET /api/v1/chats/?page=1
#1625 requests naming page=2 = 0
```

## Frames

| File | Shows |
| --- | --- |
| `1626-01-composer-default-model.png` | A brand new conversation at 1440x900. The composer chip reads `hive-default`, and nothing sits beside it: the plus that used to add a second model is gone. |
| `1626-02-picker-open-no-plus.png` | The picker open on the same conversation. One selection, a tick against `hive-default`, and `deepseek-v4-flash` visible above it as the entry the old alphabetical fallback picked. |
| `1626-03-two-model-url-clamped.png` | `/?models=hive-fast,hive-free`, the other route by which a multi-model list used to arrive. One chip, reading `hive-fast`: the drawn model is the dispatched model. |
| `1619-01-mobile-typed.png` | 390x844, `isMobile` and `hasTouch` both on, text typed and not yet sent. |
| `1619-02-mobile-after-enter.png` | The same viewport after one Enter press. The message is sent and answered. Before this change the keydown branch was unreachable on exactly this viewport and Enter only inserted a newline. |
| `1625-01-sidebar-chat-list.png` | The left nav after a cold reload, six conversations rendered. The request log above shows the only chat list call made during that reload was `page=1`. |

Images are attached to the pull request through
`scripts/post-pr-visual-proof.sh`, which uploads them to the permanent
`visual-proof-assets` release rather than to this branch.

## The `ui.default_models` half, proved as a mechanism rather than a first boot

`DEFAULT_MODELS` is persistent config, so a compose value alone is a no-op on a
box that has already booted once. The container was therefore booted twice
against the same data volume.

Boot one, with `DEFAULT_MODELS` unset:

```
$ curl -s http://127.0.0.1:3401/api/config | ... default_models
default_models = None
```

Boot two, same volume, with `DEFAULT_MODELS=hive-default` added:

```
open_webui.config:seed_registered_defaults - hive: reconciled Open WebUI config from env:
  ... ui.default_models=hive-default ...
```

and `/api/config`, read as a signed-in user, then answers `"hive-default"`.

`hive-default` is what the DEPLOYED box receives, from `deploy-demo-box.yml`'s
`OWUI_DEFAULT_MODEL`. `docker-compose.yml`'s committed default is the
upstream-free `hive-free`, so no pipeline that starts a stack from that file
opens its conversations on a paid alias. This capture therefore exercises the
deployed value, which is the one issue #1626 is about. The split arrived after
the first push, when `TestNoCISurfaceCallsAPaidCompletionModel` correctly
flagged a paid completion alias committed to a CI surface.
Read unauthenticated it still answers nothing at all, because `main.py` only
includes `default_models` in the payload for a signed-in user; that is upstream
behaviour and not a symptom.

## Automated checks run alongside

```
make test-owui-frontend                          24 test files, 349 tests, all passing
make test-scripts                                ok
python3 scripts/test_owui_rag_env_config.py      ok
python3 scripts/test_owui_chat_list_page_size.py ok
npm run lint:proof-tokens                        ok
```

The new guards would have failed on `origin/main`, checked directly:
`ModelSelector.svelte` carried two `Add Model` occurrences, `MessageInput.svelte`
two `ontouchstart` occurrences, and `Chat.svelte` ten calls passing the live
pagination cursor to a whole-list refresh.

---

# Re-capture after the independent review moved the clamp (2026-09-01, second pass)

The review found that the one-model clamp, then living in `ModelSelector.svelte`,
fired `Chat.svelte`'s `savedModelIds` and PATCHed a folder's `model_ids` down to a
single entry on page load. The clamp has moved into `Chat.svelte`, where the array
is owned, and the folder save now refuses to narrow. Both are behaviour changes, so
the stack was rebuilt from the updated branch and re-captured rather than reasoned
about.

Rig identical to the first pass, on its own port and its own network so a peer
agent's container could not be mistaken for it. The first attempt to reuse port
3402 answered healthy from somebody else's stack, which is why the port is
explicit here.

```
signed in (local throwaway account, password redacted)
#1626 folder model_ids BEFORE: ["hive-fast","hive-free"]
#1626 folder model_ids AFTER opening it in the browser: ["hive-fast","hive-free"]
#1626 folder-writing requests observed during that load: 0
#1626 composer chip inside that folder reads "hive-fast" (one model drawn, folder list untouched)
#1626 with ?models=hive-fast,hive-free: chips = 1, chip reads "hive-fast"
#1626 "Add Model" buttons = 0
#1619 user messages after Enter at 390x844 = 1
```

The folder route was genuinely exercised rather than skipped, and the log shows it
from the inside: the composer chip reads `hive-fast`, the FIRST of the folder's two
model ids, which is the clamp's own output. So the clamp fired, drew one model, and
no write followed it. Zero requests to `/api/v1/folders/{id}/update` during the load.

| File | Shows |
| --- | --- |
| `a0b-1626-04-folder-not-narrowed.png` | A folder holding two model ids, opened in the browser. One chip, reading the first of them, and the folder's stored list unchanged at two. |
| `a0b-1626-05-two-model-url-clamped-in-chat.png` | `/?models=hive-fast,hive-free` with the clamp now enforced in `Chat.svelte` rather than in the chip. One chip. |
| `a0b-1619-03-mobile-after-enter.png` | 390x844 with touch on, one Enter press, message sent and answered. Unchanged by the relocation, re-captured because the composer was edited again. |

The RED for the folder write is the reviewer's static trace, not a second build of
the pre-review commit: `selectedFolder.subscribe` assigns the folder's list, the
clamp trimmed it, and `$: if (selectedModels !== null) savedModelIds()` fired on that
assignment with `!equal(...)` true. Rebuilding the superseded commit to watch the
bad PATCH happen was judged not worth ten minutes of image build; what is captured
here is the fixed behaviour, which is what the merge policy asks for.

## Automated checks, second pass

```
make test-owui-frontend    24 files, 351 tests, pass
                           14/14 lib/hive components compiled
                           22/22 lib/components components compiled   <- new
make test-scripts          pass
npm run lint:proof-tokens  pass
```

The second compile pass is the review's third MEDIUM: the five upstream components
this change mirrors into the scratch tree were never handed to the Svelte compiler.
Proved load-bearing rather than assumed, by breaking one on purpose:

```
FAIL chat/ModelSelector.svelte (85:0): `<div>` was left open
21/22 components compiled
```
