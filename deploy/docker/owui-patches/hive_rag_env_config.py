"""Make Open WebUI's environment-owned settings follow the container
environment (issues #722, #772).

Open WebUI treats every `rag.*` setting as persistent config: the environment
seeds it into Open WebUI's own database the first time a container boots, and
after that the database wins. Its `Config.seed_defaults` is explicit about it,
"Insert keys that don't yet exist in the DB. Existing DB values take precedence
over defaults", so changing a compose value has no effect on a deployment that
has already booted once. `docker-compose.yml` carried
`RAG_EMBEDDING_MODEL: "text-embedding-3-small"` from 2026-05-18 (#143) until
2026-07-26 (#447), when it became a Hive catalog alias. Every Open WebUI volume
older than that correction kept sending the OpenAI model id, which no Hive
catalog alias matches, so document RAG failed alias resolution at the gateway
with 404 `model_not_found` and no compose change could ever reach it.

This module is spliced into that startup path by
apply_rag_env_config_patch.py. The environment wins for the four keys that
point Open WebUI's embedder at the Hive gateway, the four that point its
speech-to-text at the same gateway (the Bengali dictation fix, see their entry
below), the five that point its text-to-speech there too (#997, see their
entry below), plus `ui.enable_login_form` (same mechanism, different symptom)
and the three product-surface feature flags below (#772), and for nothing
else: an administrator's other Open WebUI settings still persist normally,
which is why this is a per-key reconcile rather than
`ENABLE_PERSISTENT_CONFIG=false`.

One seam here is not a key at all. `user.permissions` is a single row holding
the whole nested permission tree, so a leaf inside it cannot be reconciled by
naming a dotted key: that would write a row nothing reads. `PERMISSION_ENV`
below carries the leaves the environment owns, and `reconcile` reads the tree,
merges them in and writes the tree back. See that table for why
`workspace.skills` is one of them.

Issue #1575: `WEB_LOADER_TIMEOUT` (PR #1570) merged into the container
environment and nowhere else, because `web.loader.timeout` was never in this
map. Read that key's own entry below before citing this as a bound that went
missing: the loader enforces it off a frozen module constant, so the 12-second
bound has been in force since it merged and what the stale row cost was the
Admin UI's honesty, not the timeout. The audit that finding triggered walked
every environment variable this
container sets against Open WebUI's own `DEFAULT_CONFIG` (not against
memory), found thirteen more silently-inert instances (`web.loader.timeout`
itself, `webui.url`, `ui.default_locale`, the security-relevant
`ui.default_user_role` and `ui.enable_signup`, `rag.top_k` and
`web.search.result_count`, `web.search.searxng_query_url`, `openai.enable`,
`ollama.enable`, `ui.enable_community_sharing`, `evaluation.arena.enable`,
and the paired `openai.api_base_urls`/`openai.api_keys` connection that is
what chat completions actually authenticate with), and fixed every one of
them below. It also found a cluster that only *looks* like the same bug:
every `oauth.*` key `utils/oauth.py` actually authenticates with is imported
as a frozen module-level constant from `open_webui.config`, not read via
`Config.get`, so those re-read the environment fresh on every container
start regardless of what the database has seeded. `ENVIRONMENT_ONLY_ENV_VARS`
near the bottom of this file records that finding, so the boot guard does
not go on to reflag them forever.

The embedding model itself is never hardcoded here. It comes from
`RAG_EMBEDDING_MODEL` (compose derives it from `OWUI_RAG_EMBEDDING_ALIAS`), so
the admin-selected alias and its dimension stay the single source of truth
(D-001). A deployment that leaves those variables unset keeps whatever its
administrator chose inside Open WebUI.

The same first-boot-wins trap applies to the product-surface feature flags
Hive turns off (#772): Notes, Calendar and Automations are `notes.enable`,
`calendar.enable` and `automations.enable` in `DEFAULT_CONFIG`, so a compose
change alone would never reach the demo box. They are reconciled here for that
reason, and as booleans rather than strings, because the non-empty string
"false" is truthy on both sides of `/api/config` and would leave every one of
those surfaces visible.
"""

import copy
import logging
import re

_logger = logging.getLogger(__name__)

# Open WebUI persisted config key -> the environment variable that owns it.
# Key names are Open WebUI's own (open_webui.config.DEFAULT_CONFIG); the
# variable names are the ones docker-compose.yml sets on the open-webui
# service.
RAG_CONFIG_ENV = {
    "rag.embedding_engine": "RAG_EMBEDDING_ENGINE",
    "rag.embedding_model": "RAG_EMBEDDING_MODEL",
    "rag.openai.api_base_url": "RAG_OPENAI_API_BASE_URL",
    "rag.openai.api_key": "RAG_OPENAI_API_KEY",
    # Speech-to-text, and the same trap for the same reason. Open WebUI's
    # `audio.stt.engine` default is the empty string, which is upstream's
    # "transcribe with my own bundled Whisper" value, and `WHISPER_MODEL` is
    # baked into the pinned image as `base`. So the chat microphone never
    # reached the Hive gateway at all: it decoded inside the Open WebUI
    # container, unmetered, invisible to the model catalog, on a model that
    # returns romanized Latin for Bengali speech at any clip length (measured
    # live 2026-08-17, and forcing a language hint did not change it). The same
    # audio through `hive-stt`, which resolves to groq/whisper-large-v3, returns
    # Bengali script. Bangladesh is the first market, so that is not a gap.
    #
    # These four are in DEFAULT_CONFIG, so a first boot seeded them and compose
    # alone would be a silent no-op on any already-booted deployment, exactly
    # like #722. The language of the audio is deliberately NOT set here: it stays
    # a per-user choice (Settings > Audio), because forcing one language on the
    # deployment would trade broken Bengali for broken English.
    "audio.stt.engine": "AUDIO_STT_ENGINE",
    "audio.stt.model": "AUDIO_STT_MODEL",
    "audio.stt.openai.api_base_url": "AUDIO_STT_OPENAI_API_BASE_URL",
    "audio.stt.openai.api_key": "AUDIO_STT_OPENAI_API_KEY",
    # Text-to-speech, the read-aloud speaker button (#997). Same trap as STT
    # directly above: all five keys are in DEFAULT_CONFIG, so a first boot
    # seeded `engine=""` (Open WebUI's own bundled speech synthesis), a base
    # URL of api.openai.com, an empty key, model tts-1 and voice alloy, and no
    # compose change could reach any already-booted volume after that. The
    # gateway's hive-tts alias (groq/orpheus) accepts none of those defaults:
    # alloy is rejected upstream and api.openai.com is not served by anyone.
    # The voice default must name one the provider actually has; the list the
    # UI offers comes from GET /v1/audio/voices on edge-api (see its handler).
    "audio.tts.engine": "AUDIO_TTS_ENGINE",
    "audio.tts.model": "AUDIO_TTS_MODEL",
    "audio.tts.openai.api_base_url": "AUDIO_TTS_OPENAI_API_BASE_URL",
    "audio.tts.openai.api_key": "AUDIO_TTS_OPENAI_API_KEY",
    "audio.tts.voice": "AUDIO_TTS_VOICE",
    # Not a RAG key, and the only non-RAG one here. ponytail: reusing this
    # reconcile rather than minting a second identical module, because the
    # failure is identical to #722, right down to the mechanism. Every account
    # on this deployment is SSO-only, so the email/password form Open WebUI
    # renders above "Continue with Hive" can only ever fail, and it failed
    # misleadingly: correct Hive credentials came back HTTP 400 "The email or
    # password provided is incorrect". `ENABLE_LOGIN_FORM` genuinely exists in
    # the pinned v0.10.2 image (config.py:1617) and genuinely hides the form
    # (the login bundle gates the credential fields, submit button and "or"
    # divider on features.enable_login_form), but `ui.enable_login_form` is in
    # DEFAULT_CONFIG, so seed_defaults wrote it on this deployment's first boot
    # and the database has outranked the environment ever since. Setting the
    # compose variable alone would have been another silent no-op.
    "ui.enable_login_form": "ENABLE_LOGIN_FORM",
    # Added 2026-08-24 for web search in chat. The engine is a string key, so
    # it rides the string dict: a blank WEB_SEARCH_ENGINE (what compose
    # resolves on the enterprise profile and local dev, where the feature is
    # off) yields no entry and never clobbers a persisted engine, while the
    # demo's workflow env supplies "duckduckgo".
    "web.search.engine": "WEB_SEARCH_ENGINE",
    # Chat upload type allowlist (#1405). Not enforced here: enforcement is in
    # the pinned image's own `routers/files.py.upload_file_handler`, read out
    # of the running container rather than assumed, where an extension outside
    # `rag.file.allowed_extensions` is refused with 400 before the bytes reach
    # storage.
    #
    # This is a type, not a string, and the coercion matters. See LIST_KEYS.
    #
    # The size cap is deliberately NOT here. It is derived from
    # RAG_MAX_UPLOAD_BYTES instead: see derived_upload_cap below.
    "rag.file.allowed_extensions": "RAG_ALLOWED_FILE_EXTENSIONS",
    # The prompts Open WebUI sends to a model on its own account, separately
    # from anything the user typed: the chat title, the tag list, the
    # follow-up chips, the retrieval and web-search query it writes for
    # itself, the autocomplete and voice prompts, the prompt-based
    # tool-calling preamble, the context-compaction instruction, and the
    # wrapper placed around retrieved documents on the chat surface.
    #
    # The same first-boot-wins trap for the fourth time, and read out of the
    # demo box's own database on 2026-08-29 rather than assumed: every one of
    # these ten rows already exists there, `rag.template` holding upstream's
    # full default text and the nine `*.prompt_template` rows holding "", all
    # written at that volume's first boot. So compose alone could never have
    # moved any of them.
    #
    # What made this worth reconciling rather than leaving alone is that there
    # is no other way in at all. Open WebUI's admin panel, which is where
    # upstream edits these, is deleted from the fork's source and 404'd at the
    # proxy, and every write verb under /api/v1/configs is denied there too, so
    # the only remaining path was a hand-written SQLite UPDATE inside the
    # owui-data volume on a live box.
    #
    # The empty string is not "no template": each consumer substitutes its own
    # DEFAULT_*_PROMPT_TEMPLATE when the persisted value is falsy, so the
    # shipped prompt text lives in Python and the row is an override slot.
    # `rag.template` is the exception, its default IS the text.
    #
    # Every variable name here is upstream's own (config.py's own os.getenv
    # calls), so an operator reading Open WebUI's documentation finds the same
    # name. compose passes all ten through with an empty default on every
    # profile, so a deployment that sets nothing keeps today's prompts byte
    # for byte. See TEMPLATE_KEYS below for why these alone are not stripped.
    "rag.template": "RAG_TEMPLATE",
    "task.title.prompt_template": "TITLE_GENERATION_PROMPT_TEMPLATE",
    "task.tags.prompt_template": "TAGS_GENERATION_PROMPT_TEMPLATE",
    "task.image.prompt_template": "IMAGE_PROMPT_GENERATION_PROMPT_TEMPLATE",
    "task.follow_up.prompt_template": "FOLLOW_UP_GENERATION_PROMPT_TEMPLATE",
    "task.query.prompt_template": "QUERY_GENERATION_PROMPT_TEMPLATE",
    "task.autocomplete.prompt_template": "AUTOCOMPLETE_GENERATION_PROMPT_TEMPLATE",
    "task.voice.prompt_template": "VOICE_MODE_PROMPT_TEMPLATE",
    "task.tools.prompt_template": "TOOLS_FUNCTION_CALLING_PROMPT_TEMPLATE",
    "chat.context_compaction.prompt_template": "CONTEXT_COMPACTION_PROMPT_TEMPLATE",
    # Issue #1575 audit. Same trap, found by walking every environment
    # variable this container sets against Open WebUI's own DEFAULT_CONFIG
    # instead of trusting memory. `routers/openai.py.get_openai_connection`
    # and `routers/retrieval.py`'s per-request `get_retrieval_config()` both
    # read these straight from the Config store on every call, so a database
    # that seeded any of them before this deployment's compose value existed
    # keeps answering with the stale one forever, exactly like #722.
    #
    # WEB_LOADER_TIMEOUT is the one that motivated the audit, and what
    # reconciling it buys is narrower than issue #1575 claimed. The stale row
    # is real: confirmed on the demo box (deploy run 33341365483),
    # `web.loader.timeout` held `""`, 23 days old. The enforcement path does
    # not read that row, though. retrieval/web/utils.py:48 imports
    # WEB_LOADER_TIMEOUT straight from open_webui.config, where it is a plain
    # os.getenv rather than a PersistentConfig, and both enforcement sites
    # (utils.py:861 SafeWebBaseLoader, utils.py:899 microsoft_web_iq) read
    # that frozen module constant. PR #1570's 12-second bound has therefore
    # been in force since it merged, on the container environment alone.
    #
    # What the stale row does reach is the per-request RetrievalConfig
    # routers/retrieval.py builds, which is what the admin API displays
    # (:741, :1437) and writes (:1290). So reconciling this stops the Admin UI
    # from reporting a value that disagrees with what is enforced. Do not let
    # anything come to depend on the row FOR enforcement, and do not drop
    # WEB_LOADER_TIMEOUT from the container environment on the grounds that
    # the row is now correct: either one silently unbounds the loader.
    # Deliberately absent from INT_KEYS too, since upstream types the field
    # `str | None` and wraps both reads in try/except ValueError, so a string
    # is the right shape here.
    "web.loader.timeout": "WEB_LOADER_TIMEOUT",
    "web.search.searxng_query_url": "SEARXNG_QUERY_URL",
    "webui.url": "WEBUI_URL",
    # ui.default_locale is cosmetic; ui.default_user_role is not. It is the
    # role a brand-new account gets, on both the password and the OAuth
    # signup paths (routers/auths.py), and docker-compose.yml sets it to
    # "pending" deliberately so an unaffiliated login lands on the
    # activation-pending screen rather than being granted app access. A
    # stale persisted "user" here would silently reopen that door.
    "ui.default_locale": "DEFAULT_LOCALE",
    "ui.default_user_role": "DEFAULT_USER_ROLE",
    # Both are read fresh on every request (routers/retrieval.py's
    # get_retrieval_config(), and the web-search call site for the result
    # count), and both are integers upstream (int(os.getenv(...))), so
    # neither BOOLEAN_KEYS nor LIST_KEYS coercion applies -- see INT_KEYS
    # below for the numeric coercion these two need instead.
    "rag.top_k": "RAG_TOP_K",
    "web.search.result_count": "WEB_SEARCH_RESULT_COUNT",
    # Issue #1609, the two knobs that decide the SHAPE of a document's
    # embedding traffic rather than its destination. Upstream defaults both to
    # the shape that took web search down: RAG_EMBEDDING_BATCH_SIZE is 1, so
    # one HTTP request carries one chunk, and
    # RAG_EMBEDDING_CONCURRENT_REQUESTS is 0, which get_embedding_function
    # reads as "build no semaphore" and so fires every one of those requests
    # at once through asyncio.gather (retrieval/utils.py). Five fetched pages
    # chunk to tens of chunks, so one web search opened tens of simultaneous
    # POSTs to the gateway embeddings route, edge-api refused part of the
    # burst with 429, and the search answered with no sources at all.
    #
    # Reconciled rather than only set in docker-compose.yml, and this half is
    # the load-bearing one: both are persisted config, so a volume that has
    # already booted keeps the value its first boot seeded and no compose
    # change reaches it. The demo box has been up since long before either
    # variable appeared in compose, so unless an administrator has since
    # changed them in Settings it is holding 1 and 0 today.
    #
    # Both are read live, not frozen, so reconciling them at startup is
    # sufficient: RETRIEVAL_CONFIG_KEYS in routers/retrieval.py maps both
    # names to these keys, and get_retrieval_config() rebuilds its
    # RetrievalConfig from Config.get_many on every call, which is the object
    # save_docs_to_vector_db hands to get_embedding_function for each
    # document.
    "rag.embedding_batch_size": "RAG_EMBEDDING_BATCH_SIZE",
    "rag.embedding_concurrent_requests": "RAG_EMBEDDING_CONCURRENT_REQUESTS",
    # Corrected post-review (PR #1582): these five were first placed on
    # ENVIRONMENT_ONLY_ENV_VARS below on the false premise that utils/oauth.py
    # reads every oauth.* value as a frozen module constant. Two of them,
    # `oauth.group_claim`/`oauth.roles_claim`, are read live instead, through
    # `get_oauth_runtime_config()` (utils/oauth.py:180, backed by
    # `OAUTH_RUNTIME_CONFIG` at :121), which `handle_login`, `handle_callback`
    # and `get_user_role` all call on every SSO login. `oauth.provider_url`
    # is not on that path but has its own separate live read, a fallback in
    # `/signout` when the session's own registered OIDC metadata lookup comes
    # back empty (routers/auths.py:926). Reconciled here rather than left
    # exception-noted, since fixing it is no more code than documenting it.
    "oauth.group_claim": "OAUTH_GROUPS_CLAIM",
    "oauth.roles_claim": "OAUTH_ROLES_CLAIM",
    "oauth.provider_url": "OPENID_PROVIDER_URL",
    # Persisted as a list of role/group identifiers, upstream's own comma
    # split (`OAUTH_ALLOWED_ROLES = [role.strip() for role in os.getenv(...)
    # .split(OAUTH_ROLES_SEPARATOR)]`, config.py:2495), consumed by
    # `OAuthManager.get_user_role` (utils/oauth.py:1421-1470) as the allow
    # list and admin list an incoming SSO role claim is matched against.
    # Coerced via COMMA_LIST_KEYS below, a separate rule from LIST_KEYS
    # above (file extensions): role and group identifiers are case-sensitive
    # ("ADMIN" must not become "admin") and carry no leading dot to strip,
    # so LIST_KEYS' lowercasing would silently break every role match.
    "oauth.allowed_roles": "OAUTH_ALLOWED_ROLES",
    "oauth.admin_roles": "OAUTH_ADMIN_ROLES",
    # 2026-08-30 outage (PR #1587 is the incident revert; see that PR and this
    # one's body for the timeline). guard_unreconciled_env_vars correctly
    # caught these two as unreconciled and, at the time, correctly aborted
    # the boot -- the bug was the guard's own fatal-at-boot posture, fixed
    # below, not a misclassification. They belong here, not on
    # ENVIRONMENT_ONLY_ENV_VARS: neither is a frozen module constant.
    #
    # Both variables are baked into the pinned image itself, not set by this
    # repo's docker-compose.yml at all (confirmed: neither name appears
    # there). vendor/open-webui's own Dockerfile sets
    # `ENV TIKTOKEN_ENCODING_NAME="cl100k_base"` and
    # `ENV WHISPER_MODEL="base"` (used at build time to pre-download the
    # Whisper model and pre-cache the tiktoken encoding for offline use), so
    # every container this image starts has both present in os.environ
    # regardless of what compose sets, which is exactly what tripped the
    # guard on the box: not a compose change, an image default that was
    # always there.
    #
    # `rag.tiktoken_encoding_name` is read live, not frozen: it reaches
    # `open_webui.routers.retrieval` through `RETRIEVAL_CONFIG_KEYS`
    # (`'TIKTOKEN_ENCODING_NAME': 'rag.tiktoken_encoding_name'`), and
    # `get_retrieval_config()` builds its `RetrievalConfig` from
    # `Config.get_many(*RETRIEVAL_CONFIG_KEYS.values())` on every call, the
    # same DB-backed path already established for `rag.top_k` above. Every
    # consumer (`get_splitter_length_function`, the token-splitter branch of
    # `save_docs_to_vector_db`) reads `config.TIKTOKEN_ENCODING_NAME` off
    # that per-request object, never off the frozen `config.py` module
    # constant directly.
    #
    # `audio.stt.whisper_model` is the same shape: `routers/audio.py`'s
    # `STT_CONFIG_KEYS` maps `'WHISPER_MODEL': 'audio.stt.whisper_model'`,
    # and its own `get_config_values()` helper (module-local, same pattern as
    # retrieval's) reads it via `Config.get_many`. `WHISPER_MODEL_DIR` and
    # `WHISPER_MODEL_AUTO_UPDATE` are the genuinely frozen siblings here
    # (imported directly from `open_webui.config`, config.py:1516-1517), which
    # is why only the model name is reconciled and the other two are left
    # alone.
    #
    # Consequence, stated plainly, because "a deployment that sets neither"
    # is not a case that exists for this image: both rows are now rewritten
    # from the image's build-time ENV on every boot, so the Admin UI fields
    # behind them (Admin > Audio for the STT model, Admin > Documents for the
    # encoding) stop being durable. That is the deliberate bargain of this
    # module for a key a deployment chose to set in compose, and it is a
    # weaker bargain for a value that is a build-time pre-caching artifact
    # nobody chose deployment-wise. Accepted here because the blast radius
    # today is nil: the reconciled value equals the DEFAULT_CONFIG seed in
    # both cases (`cl100k_base`, `base`), `audio.stt.whisper_model` is only
    # consulted when the STT engine is local faster-whisper and compose pins
    # AUDIO_STT_ENGINE to "openai", and `rag.tiktoken_encoding_name` is only
    # consulted on the token-splitter path, which compose does not select.
    # That trade, not "does a deployment set it", is the test for whether some
    # other image-baked ENV belongs on this map.
    "rag.tiktoken_encoding_name": "TIKTOKEN_ENCODING_NAME",
    "audio.stt.whisper_model": "WHISPER_MODEL",
    # The eleventh prompt key, and the only Hive-owned one: no upstream key,
    # no upstream variable, no DEFAULT_CONFIG entry (issue #1596).
    #
    # The chat surface had no system prompt at all. Open WebUI's only two
    # inputs are `params.system` on a row of its own `models` table and the
    # per-user Settings > General field, and neither is reachable for a Hive
    # model: the listing is synthesized by hive_model_picker.py from the
    # control-plane catalog, which has no system-prompt column and leaves no
    # durable Open WebUI row to carry one. So no identity, no capability
    # statement, no citation rule and no refusal guidance shipped, and the
    # customer was talking to whatever default the routed upstream applies.
    #
    # The row is consumed by apply_chat_system_prompt_patch.py, which prepends
    # it to the request's system message inside process_chat_payload. It rides
    # this reconcile rather than being read straight from os.environ in that
    # patch for one reason worth the row: the boot line this module logs is
    # what proves the configured value reached the deployment, which is the
    # evidence standard the #1596 spec sets for every prompt it moves.
    #
    # It is in TEMPLATE_KEYS below for the same reason the other ten are, so
    # the persisted value is byte for byte what the environment wrote.
    "hive.chat.system_prompt": "HIVE_CHAT_SYSTEM_PROMPT",
}

# Same idea, boolean-valued.
#
# The first three are the stock surfaces Hive does not ship (#772). Every gate
# on them is `$config.features.enable_* && (role === 'admin' || <permission>)`,
# so unlike the workspace surfaces the feature flag alone hides them from
# administrators too, and no bundle rewrite is needed.
#
# `rag.enable_hybrid_search` is here for #832, and it is the same first-boot
# trap rather than a surface: the demo box had it persisted as true from its
# very first boot, so the compose default could never move it. Leaving it true
# while no reranking model is configured is what made every knowledge-backed
# answer time out, because Open WebUI's RerankCompressor falls back to
# re-embedding the query and every retrieved document when it has no reranker
# (retrieval/utils.py). docker-compose.yml carries the reasoning and the
# measured numbers.
FEATURE_CONFIG_ENV = {
    "notes.enable": "ENABLE_NOTES",
    "calendar.enable": "ENABLE_CALENDAR",
    "automations.enable": "ENABLE_AUTOMATIONS",
    # Added 2026-08-17, same mechanism and same first-boot trap. Upstream's
    # Memory feature, on by default, whose only surface here is the Settings >
    # Personalization tab; Hive's memory subsystem is a separate thing that is
    # not built yet (.wolf/decisions.md D-020). Dockerfile.open-webui now also
    # ships the image with this off, but the demo box seeded `memories.enable`
    # true on its first boot in 2026 and the row has outranked the image ever
    # since, so this entry is what actually turns it off there.
    "memories.enable": "ENABLE_MEMORIES",
    "rag.enable_hybrid_search": "ENABLE_RAG_HYBRID_SEARCH",
    # Added 2026-08-17. Sends an unauthenticated visitor straight to the one
    # configured provider instead of rendering a page whose only control is
    # "Continue with Hive". `oauth.auto_redirect` is in DEFAULT_CONFIG, so the
    # demo box seeded it false on its first boot and the compose variable alone
    # would be another silent no-op, the same trap as #722 and #772. The sign in
    # page still refuses to redirect on its own when the deployment is not
    # unambiguously SSO only, so this flag turns the behaviour on rather than
    # forcing it.
    "oauth.auto_redirect": "OAUTH_AUTO_REDIRECT",
    # Added 2026-08-24 for web search in chat. Same first-boot-wins trap,
    # confirmed against the demo box's own database before writing this: the
    # box's config table has carried web.search.enable = false (and an empty
    # engine) since its first boot, so the compose passthrough from #414 and
    # the workflow-env flip in deploy-demo-box.yml would both have been silent
    # no-ops in production. These three follow the container environment for
    # the same reason the product-surface flags above do: the deployment's
    # posture (search on or off, which engine) is a deployment decision, not
    # an Open WebUI admin setting. The demo turns search on via workflow env;
    # the enterprise profile keeps its compose defaults off, and its opt-in
    # ruling stands.
    "web.search.enable": "ENABLE_WEB_SEARCH",
    "web.search.bypass_web_loader": "BYPASS_WEB_SEARCH_WEB_LOADER",
    "web.search.bypass_embedding_and_retrieval": (
        "BYPASS_WEB_SEARCH_EMBEDDING_AND_RETRIEVAL"
    ),
    # Issue #1575 audit, continued (booleans this time).
    #
    # ui.enable_signup gates self-registration in routers/auths.py (`if not
    # await Config.get('ui.enable_signup') or not await
    # Config.get('ui.enable_login_form')`). This deployment is SSO-only and
    # docker-compose.yml sets ENABLE_SIGNUP=false for that reason; a stale
    # persisted true here would silently reopen self-registration next to
    # the "Continue with Hive" button, the same posture ui.enable_login_form
    # above already closed for the login-form half of that page.
    "ui.enable_signup": "ENABLE_SIGNUP",
    # openai.enable, and the paired openai.api_base_urls/openai.api_keys
    # reconciled by openai_connection_override below, are the connection
    # Open WebUI's own chat completion path calls on every turn
    # (routers/openai.py.get_openai_connection, which reads Config.get_many
    # fresh per request): this is the wiring that points chat at the Hive
    # gateway (http://edge-api:8080/v1, OWUI_SHIM_KEY) at all, and it is
    # exactly as exposed to #722 as the RAG embedder keys above.
    # ollama.enable gates the parallel Ollama router family the same way
    # (routers/ollama.py, a dozen `if not await Config.get('ollama.enable')`
    # guards); compose sets it false to keep that surface unreachable.
    "openai.enable": "ENABLE_OPENAI_API",
    "ollama.enable": "ENABLE_OLLAMA_API",
    "ui.enable_community_sharing": "ENABLE_COMMUNITY_SHARING",
    "evaluation.arena.enable": "ENABLE_EVALUATION_ARENA_MODELS",
    # Corrected post-review (PR #1582), CRITICAL: originally placed on
    # ENVIRONMENT_ONLY_ENV_VARS below, on the same false premise as the three
    # string keys documented next to it in RAG_CONFIG_ENV. All three are read
    # live via `get_oauth_runtime_config()` (utils/oauth.py:180), and
    # `oauth.enable_role_mapping` gates the role-escalation block in
    # `OAuthManager.get_user_role` (utils/oauth.py:1418) that promotes an
    # incoming SSO login to `admin` when its role claim matches
    # `oauth.admin_roles`. docker-compose.yml sets all three today
    # (ENABLE_OAUTH_SIGNUP=true, ENABLE_OAUTH_ROLE_MANAGEMENT=true,
    # ENABLE_OAUTH_GROUP_MANAGEMENT=true), so this was a live exposure, not
    # only a future-drift risk: a `owui-data` volume seeded before or
    # differently from these compose values would have silently ignored any
    # later correction to the SSO admin-role mapping, in the highest-severity
    # area this whole issue is about (see also issue #1511, a prior live
    # admin-bypass in this same claim-mapping area).
    "oauth.enable_signup": "ENABLE_OAUTH_SIGNUP",
    "oauth.enable_role_mapping": "ENABLE_OAUTH_ROLE_MANAGEMENT",
    "oauth.enable_group_mapping": "ENABLE_OAUTH_GROUP_MANAGEMENT",
}

# The user permission tree, which is ONE persisted row and therefore cannot
# ride either dictionary above.
#
# `user.permissions` (open_webui.config.DEFAULT_CONFIG) holds the whole nested
# permission tree in a single config row, and `Config.upsert` keys rows by the
# exact string handed to it. Adding "user.permissions.workspace.skills" to
# RAG_CONFIG_ENV would therefore succeed, persist, survive a restart, and be
# read by nothing: `utils/access_control.has_permission` splits the permission
# key and walks the tree inside the one row, so it never looks at a sibling
# row. A row nothing reads is worse than no row, because the deployment then
# looks configured. So this seam reads the tree, merges the leaves the
# environment names, and writes the whole tree back.
#
# `workspace.skills` is here because the chat product now ships a user-created
# skills library and the permission that gates it defaults to false upstream.
# It matters now and did not before: until 2026-08-23 every tenant OWNER was
# promoted to Open WebUI `admin` and passed every `role === 'admin' || <perm>`
# gate regardless, and since
# supabase/migrations/20260823_03_owui_role_never_admin.sql only a platform
# admin is, so this permission governs every ordinary customer.
#
# Sharing is deliberately NOT here. `sharing.skills` and
# `sharing.public_skills` stay at their upstream defaults, both false, so a
# skill is private to the account that wrote it and no member can put authored
# text into another account's prompt.
PERMISSION_ENV = {
    ("workspace", "skills"): "USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS",
}

# The single row every entry in PERMISSION_ENV lives inside.
PERMISSIONS_KEY = "user.permissions"

# Keys Open WebUI stores as a JSON boolean rather than a string. The value has
# to be coerced on the way in: `features.enable_login_form` is published raw to
# the browser, and the login page tests it for truthiness, so a persisted
# string "false" is truthy there and would render the very form this is meant
# to remove. Open WebUI's own parse is `os.getenv(...).lower() == 'true'`
# (config.py:1617), and this matches it so the reconciled value is identical to
# the one a first boot would have seeded.
BOOLEAN_KEYS = frozenset({"ui.enable_login_form"})

# Keys Open WebUI stores as a JSON list of strings, split on commas exactly as
# upstream's own parse does
# (`[ext.strip() for ext in os.getenv(...).split(',') if ext.strip()]`).
#
# This coercion is load bearing rather than cosmetic. `upload_file_handler`
# evaluates `file_extension not in allowed_file_extensions`, so persisting the
# raw comma string would silently turn a membership test into a SUBSTRING test:
# an upload with extension `df` would be admitted because the string "pdf"
# contains it, and so would every other extension that happens to be a
# substring of an allowed one. Lowercased and stripped of any leading dot on
# the way in, because the handler compares against an extension it has already
# lowercased and stripped the dot from.
LIST_KEYS = frozenset({"rag.file.allowed_extensions"})

# Keys Open WebUI stores as a JSON integer, coerced the same way upstream's
# own parse does (`int(os.getenv(...))`). A raw string here is not merely
# cosmetic: the first two flow into a `k=` argument the retrieval/search call
# sites pass straight to a vector-store query (issue #1575 audit), and the
# other two are used as arithmetic, a slice bound and a semaphore size, both
# of which raise TypeError on a string. So leaving any of them a string risks
# an error deep in a request path rather than a config value simply being
# ignored.
INT_KEYS = frozenset(
    {
        "rag.top_k",
        "web.search.result_count",
        "rag.embedding_batch_size",
        "rag.embedding_concurrent_requests",
    }
)

# The INT_KEYS members that must additionally be at least 1 (issue #1609).
# Zero is a legal integer for both and a broken value for both, in opposite
# directions. rag.embedding_batch_size reaches range(0, len(texts), 0) inside
# get_embedding_function, which raises on every document;
# rag.embedding_concurrent_requests treats 0 as "no semaphore at all", which
# is the unbounded burst this issue exists to end.
#
# What "refused" means here, precisely, because the two callers differ. Under
# `fatal=True` (the CI/self-check path) a zero raises. Under `fatal=False`,
# which is what the boot splice deliberately uses since the 2026-08-30
# outage, `_refuse` logs at ERROR and the key is skipped, so the boot starts
# normally and the persisted row keeps whatever it already held -- which, on
# a volume seeded from DEFAULT_CONFIG, is the unbounded 0. So this rejects the
# write, it does not repair the row and it does not stop the container.
#
# Clamping to 1 on the non-fatal path was considered and rejected: 1 is the
# correct floor for the concurrency knob but is exactly the unbatched,
# one-request-per-chunk batch size that caused this defect, so a single clamp
# constant would silently re-arm one half of the fix while appearing to
# repair it. An operator who wants a bound names a positive number.
POSITIVE_INT_KEYS = frozenset(
    {"rag.embedding_batch_size", "rag.embedding_concurrent_requests"}
)

# Keys Open WebUI stores as a JSON list, comma-split like LIST_KEYS but
# case-preserving and with no dot to strip: role/group identifiers, not file
# extensions. See the oauth.allowed_roles/oauth.admin_roles entries above.
COMMA_LIST_KEYS = frozenset({"oauth.allowed_roles", "oauth.admin_roles"})

# Keys whose value is a prompt, and therefore the only ones here that are
# persisted exactly as the environment wrote them.
#
# Every other key in this module is stripped on the way in, which is right for
# a model id or a URL and wrong for a prompt: leading indentation and a
# trailing newline are part of what most of these consumers hand to the model,
# and upstream's own read of these variables is a bare `os.getenv` with no
# strip at all. Stripping here would persist a value that differs from the one
# a first boot would have seeded, which is the class of mismatch that made
# `ui.enable_login_form` wrong before.
#
# Two of the ten do strip again at the point of use, so for those this is
# merely harmless rather than load bearing: `rag_template` (utils/task.py) and
# the context-compaction prompt (utils/context_compaction.py) each test the
# stripped value before falling back to their own default. Named rather than
# glossed over, because "nothing downstream strips" would be a tidier claim
# and an inaccurate one, and an inaccurate comment is what the next person
# builds a wrong assumption on.
#
# The strip still decides whether the variable was SET, so an all-whitespace
# value stays "unset" exactly like every other key here rather than persisting
# a blank prompt. That distinction is load bearing: a blank row is falsy, and
# every consumer treats a falsy row as "use my built-in default", so persisting
# one would silently revert the prompt while leaving the deployment looking
# configured.
TEMPLATE_KEYS = frozenset(
    {
        "rag.template",
        "task.title.prompt_template",
        "task.tags.prompt_template",
        "task.image.prompt_template",
        "task.follow_up.prompt_template",
        "task.query.prompt_template",
        "task.autocomplete.prompt_template",
        "task.voice.prompt_template",
        "task.tools.prompt_template",
        "chat.context_compaction.prompt_template",
        # Hive-owned, not upstream. See its entry in RAG_CONFIG_ENV.
        "hive.chat.system_prompt",
    }
)

# Keys whose value must never be logged. The rest are named with their value,
# because the embedding model Open WebUI will actually send is the one signal
# this failure mode never produced anywhere: Open WebUI logs only aiohttp's
# bare "404, message='Not Found'" for it, having discarded the response body
# that names the model.
SECRET_KEYS = frozenset(
    {
        "rag.openai.api_key",
        "audio.stt.openai.api_key",
        "audio.tts.openai.api_key",
        # OWUI_SHIM_KEY, a real registered Hive API key. Unlike the three
        # above this one is a list (openai_connection_override), so
        # `rendered` below only needs to withhold its value, not reshape it.
        "openai.api_keys",
    }
)

# Destination keys that must never be written without their credential, and the
# environment variables an operator has to fix when one is missing. Open WebUI
# persists the destination and the credential as two independent rows, and this
# module only writes the keys the environment supplies, so a destination on its
# own would repoint the call while Open WebUI kept sending the key persisted for
# the previous destination.
PAIRED_DESTINATIONS = (
    ("rag.openai.api_base_url", "rag.openai.api_key", "Open WebUI's embedder"),
    (
        "audio.stt.openai.api_base_url",
        "audio.stt.openai.api_key",
        "Open WebUI's speech-to-text",
    ),
    (
        "audio.tts.openai.api_base_url",
        "audio.tts.openai.api_key",
        "Open WebUI's text-to-speech",
    ),
)


# The one settable upload ceiling in the whole stack, in bytes (issue #1428).
# docker-compose.yml interpolates the identical `${RAG_MAX_UPLOAD_BYTES:-...}`
# expression into edge-api, into the markitdown sidecar as MAX_UPLOAD_BYTES,
# and into this container.
UPLOAD_CEILING_ENV = "RAG_MAX_UPLOAD_BYTES"

# The knob this replaces, kept named so its return can be refused rather than
# silently obeyed.
SUPERSEDED_UPLOAD_CAP_ENV = "RAG_FILE_MAX_SIZE"

BYTES_PER_MEGABYTE = 1024 * 1024


def _refuse(message: str, fatal: bool) -> None:
    """Refuse a value: raise in CI, log and skip at boot.

    The 2026-08-30 outage was `guard_unreconciled_env_vars` aborting FastAPI
    startup over a config-hygiene finding. `overrides` had eight raises of the
    same shape one line further down the same boot splice, so making only the
    guard non-fatal would have left seven other ways for one bad environment
    variable to crash-loop chat (PR #1588 review).

    At `fatal=True`, which is the default and what CI uses, the message is a
    RuntimeError and a red build. At `fatal=False`, which the boot splice uses,
    it is an ERROR log and the caller skips that one key, so the persisted row
    keeps the value the previous boot left in it. Skipping is the safe
    degradation in every case here, and deliberately so: it is the exact
    contract this module already has for a variable that is not set at all, and
    for the two allowlist keys it is strictly safer than the alternative, since
    writing the empty list the caller parsed would turn the check off while the
    deployment still looked configured.
    """
    if fatal:
        raise RuntimeError(message)
    _logger.error(message)


def derived_upload_cap(environ, fatal: bool = True) -> dict:
    """Derive `rag.file.max_size` from the one settable upload ceiling.

    Open WebUI wants whole megabytes; edge-api and the markitdown sidecar want
    bytes. That unit mismatch is the entire reason this is code rather than a
    plain compose interpolation like the other two, and it used to be an excuse
    for a second variable: `RAG_FILE_MAX_SIZE` on the chat surface, set
    independently of `RAG_MAX_UPLOAD_BYTES` on the ingest path, in different
    units, with nothing making them agree.

    That produced issue #1428. PR #1426 set the compose default to 25 to match
    the ingest ceiling byte for byte, correctly, and the deployment defeated it:
    `/home/sakib/hive/.env` on the box carried `RAG_FILE_MAX_SIZE=100`, an
    explicit value beats a compose fallback, and the chat surface went on
    publishing a 100 MB cap to every browser while edge-api and the sidecar
    refused anything over 25 MB. Measured on the deployed chat 2026-08-29, with
    the derived cap absent: a 30 MB attachment produced a chip, a POST, and no
    response at all in 44 seconds of watching, while the same file sent
    straight at the container's API was accepted and stored with a 200.

    A check that compared the two would have reported the disagreement and left
    both knobs in place. Deriving one from the other means the disagreement
    cannot be written down.

    Rounds DOWN. The chat surface must never accept a file the ingest path
    would refuse, so a byte value that is not a whole number of megabytes
    yields the smaller cap, not the larger.

    Raises RuntimeError rather than falling back, in three cases: the
    superseded variable is present at all, the ceiling cannot be parsed, or it
    floors to less than one megabyte. Sub-megabyte is refused for a sharper
    reason than "not a useful cap": the two consumers read 0 oppositely. Open
    WebUI's server-side check is `if max_size and len(contents) > ...`, where 0
    is falsy and enforces nothing, while the browser's is
    `file.size > max_size * 1024 * 1024`, where 0 rejects every file of
    non-zero length. A deployment there would refuse every upload in the
    composer while leaving the API accepting files of unlimited size, and would
    read as a working cap.
    """
    superseded = (environ.get(SUPERSEDED_UPLOAD_CAP_ENV) or "").strip()
    if superseded:
        _refuse(
            f"{SUPERSEDED_UPLOAD_CAP_ENV} is set to {superseded!r}, and it is no "
            f"longer read. The chat attachment cap is derived from "
            f"{UPLOAD_CEILING_ENV}, the same ceiling edge-api and the markitdown "
            f"sidecar enforce, so that one deployment cannot accept in chat what "
            f"it refuses everywhere else (issue #1428). Remove "
            f"{SUPERSEDED_UPLOAD_CAP_ENV} and set {UPLOAD_CEILING_ENV} in bytes "
            f"instead. Refusing to start rather than quietly ignoring it, "
            f"because being quietly ignored is exactly what this variable did "
            f"for the three deploys before this one.",
            fatal,
        )
        return {}

    raw = (environ.get(UPLOAD_CEILING_ENV) or "").strip()
    if not raw:
        # Same contract as every other key here: an unset variable writes
        # nothing, so an administrator's own choice survives and a deployment
        # that never sets it is not silently capped. Compose always supplies a
        # value, so this is the enterprise/self-host path, not the demo one.
        return {}

    # isascii() as well as isdigit(), because str.isdigit() is true for
    # characters int() then refuses: "²" (superscript two) is a digit by
    # that test and a ValueError to int(), which would escape this function as
    # an unhandled traceback instead of the RuntimeError below. Other Unicode
    # digits, "٣" for instance, parse fine and would silently configure a
    # ceiling nobody can grep for.
    if not (raw.isascii() and raw.isdigit()):
        _refuse(
            f"{UPLOAD_CEILING_ENV} must be a whole number of BYTES, got "
            f"{raw!r}. A value that cannot be parsed would leave the deployment "
            f"looking capped while enforcing nothing, which is the silent no-op "
            f"this module exists to end. 26214400 is 25 MB.",
            fatal,
        )
        return {}

    megabytes = int(raw) // BYTES_PER_MEGABYTE
    if megabytes < 1:
        _refuse(
            f"{UPLOAD_CEILING_ENV}={raw!r} is less than one megabyte, which "
            f"floors to a chat cap of 0. Open WebUI reads 0 as no server-side "
            f"cap at all while the browser reads it as refusing every file of "
            f"non-zero length, so the composer would reject everything while "
            f"the API accepted anything. Set at least {BYTES_PER_MEGABYTE}.",
            fatal,
        )
        return {}
    return {"rag.file.max_size": megabytes}


# Issue #1575 audit: the singular Hive names for Open WebUI's own "OpenAI"
# connection, the one that actually carries chat completions to the Hive
# gateway (docker-compose.yml: OPENAI_API_BASE_URL=http://edge-api:8080/v1,
# OPENAI_API_KEY=$OWUI_SHIM_KEY).
OPENAI_CONNECTION_URL_ENV = "OPENAI_API_BASE_URL"
OPENAI_CONNECTION_KEY_ENV = "OPENAI_API_KEY"


def openai_connection_override(environ, fatal: bool = True) -> dict:
    """Reconcile Hive's one OpenAI-compatible connection, paired like
    PAIRED_DESTINATIONS reconciles rag/audio, never the base URL alone.

    Open WebUI stores this connection as parallel lists
    (`openai.api_base_urls`, `openai.api_keys`), one slot per connection an
    administrator can configure, because upstream supports several at once.
    Hive only ever wires one, under the singular names
    OPENAI_API_BASE_URL/OPENAI_API_KEY (also the names upstream's own
    OPENAI_API_BASE_URLS/OPENAI_API_KEYS fall back to when unset), so this
    persists that one connection as a single-element list rather than riding
    the plain string mapping RAG_CONFIG_ENV uses for everything else.

    Found by the #1575 audit: `routers/openai.py.get_openai_connection`
    reads `openai.api_base_urls`/`openai.api_keys` fresh from the Config
    store on every completion request, so once a deployment's database has
    seeded these two rows, an OWUI_SHIM_KEY rotation in compose never
    reaches the running chat surface and every completion keeps
    authenticating with the previous, possibly already-revoked, key.
    """
    url = (environ.get(OPENAI_CONNECTION_URL_ENV) or "").strip()
    key = (environ.get(OPENAI_CONNECTION_KEY_ENV) or "").strip()
    if not url:
        return {}
    if not key:
        _refuse(
            f"{OPENAI_CONNECTION_URL_ENV} is set to {url!r} but "
            f"{OPENAI_CONNECTION_KEY_ENV} is empty or unset. Refusing to "
            f"point Open WebUI's OpenAI connection (the Hive gateway "
            f"itself) at that destination with no credential, the same "
            f"posture PAIRED_DESTINATIONS enforces below. Set "
            f"{OPENAI_CONNECTION_KEY_ENV} (the Hive OWUI_SHIM_KEY) together "
            f"with {OPENAI_CONNECTION_URL_ENV}.",
            fatal,
        )
        return {}
    return {"openai.api_base_urls": [url], "openai.api_keys": [key]}


def overrides(environ, fatal: bool = True) -> dict:
    """Return the persisted-config overrides the environment explicitly sets.

    A missing or blank variable yields no entry, so an unset variable never
    clobbers a persisted value with an empty string.

    Raises RuntimeError when a destination is supplied without a credential.
    Only the supplied keys are written, so a base URL on its own would repoint
    the embedder while Open WebUI kept sending the API key persisted for the
    previous destination. A credential must not outlive the destination it was
    issued for, and quietly ignoring the base URL instead would leave the
    deployment diverged from its own configuration with no signal, which is the
    failure this module exists to end. So it refuses, which surfaces as a
    startup failure naming both variables rather than a silent misdirection.
    The reverse pairing is fine and is how a rotated shim key reaches Open
    WebUI: a new credential for the destination already persisted.
    """
    # First, so a superseded or malformed ceiling, or an OpenAI connection
    # missing its credential, fails the boot before anything else is
    # reconciled.
    applied = derived_upload_cap(environ, fatal)
    applied.update(openai_connection_override(environ, fatal))
    for key, variable in RAG_CONFIG_ENV.items():
        raw = environ.get(variable) or ""
        value = raw.strip()
        if not value:
            continue
        if key in TEMPLATE_KEYS:
            # Persisted unstripped, deliberately. See TEMPLATE_KEYS.
            applied[key] = raw
            continue
        if key in BOOLEAN_KEYS:
            applied[key] = value.lower() == "true"
        elif key in LIST_KEYS:
            # The leading dot is stripped because an operator writing
            # ".pdf,.txt" is writing the obvious thing, and upstream compares
            # against an extension it has already stripped the dot from, so
            # keeping the dot would persist a list that matches nothing and
            # refuse every upload while looking correctly configured.
            items = [
                item.strip().lstrip(".").lower()
                for item in value.split(",")
                if item.strip().strip(".")
            ]
            # A value that is all separators (",", " . , ") parses to an empty
            # list, and an empty list is falsy in `if process and
            # allowed_file_extensions`, so persisting it would turn the check
            # off while the deployment's own configuration says it is on.
            if not items:
                _refuse(
                    f"{variable} is set to {value!r}, which parses to no "
                    f"extensions at all. An empty allowlist turns the type "
                    f"check off entirely while the deployment still looks "
                    f"configured. Name the extensions to allow. Removing the "
                    f"allowlist altogether is a change to the compose default, "
                    f"made deliberately, not something an empty value does.",
                    fatal,
                )
                continue
            applied[key] = items
        elif key in COMMA_LIST_KEYS:
            # Case-preserving, no dot stripped: see COMMA_LIST_KEYS. Matches
            # upstream's own parse of OAUTH_ALLOWED_ROLES/OAUTH_ADMIN_ROLES
            # (config.py:2495-2502: `[role.strip() for role in
            # os.getenv(...).split(OAUTH_ROLES_SEPARATOR) if role]`).
            roles = [item.strip() for item in value.split(",") if item.strip()]
            if not roles:
                _refuse(
                    f"{variable} is set to {value!r}, which parses to no "
                    f"roles at all. An empty {key.rsplit('.', 1)[-1]} list "
                    f"disables SSO role matching entirely while the "
                    f"deployment still looks configured. Name the roles, or "
                    f"unset {variable} to leave the persisted value alone.",
                    fatal,
                )
                continue
            applied[key] = roles
        elif key in INT_KEYS:
            # Negative rejected outright, not merely non-numeric. Both
            # INT_KEYS entries are semantically counts (a vector-store/
            # search result count), and upstream's own `int(os.getenv(...))`
            # has no lower bound either, so a negative value would not raise
            # here but would still reach a request-time query as `k=-5`, the
            # exact "surfaces later instead of at boot" failure this
            # coercion exists to close (PR #1582 review).
            if not (value.isascii() and value.isdigit()):
                _refuse(
                    f"{variable} must be a whole, non-negative number, got "
                    f"{value!r}. {key} is passed straight into a "
                    f"vector-store or web-search result count, an embedding "
                    f"batch size or a concurrency bound, and a value int() "
                    f"rejects, or a negative one a query would not expect, "
                    f"would surface as an unhandled error on every request "
                    f"that reads it instead of at boot.",
                    fatal,
                )
                continue
            parsed = int(value)
            if key in POSITIVE_INT_KEYS and parsed < 1:
                _refuse(
                    f"{variable} must be at least 1, got {value!r}. Zero is "
                    f"a legal integer here and a broken one: a batch size of "
                    f"0 makes every embedding call raise, and a concurrency "
                    f"of 0 means no bound at all, which is the unbounded "
                    f"burst issue #1609 exists to end. This value is not "
                    f"written and the persisted one is left as it was, which "
                    f"may itself be an unbounded 0: name a positive number.",
                    fatal,
                )
                continue
            applied[key] = parsed
        else:
            applied[key] = value

    for key, variable in FEATURE_CONFIG_ENV.items():
        value = (environ.get(variable) or "").strip()
        if value:
            # Upstream's own parse of these variables
            # (open_webui.config: os.getenv(name, 'True').lower() == 'true'),
            # so an unrecognised value means off here exactly as it does there.
            applied[key] = value.lower() == "true"

    for url_key, credential_key, consumer in PAIRED_DESTINATIONS:
        if url_key in applied and credential_key not in applied:
            url_variable = RAG_CONFIG_ENV[url_key]
            credential_variable = RAG_CONFIG_ENV[credential_key]
            _refuse(
                f"{url_variable} is set to {applied[url_key]!r} but "
                f"{credential_variable} is empty or unset. Refusing to point "
                f"{consumer} at that destination while it keeps sending the API "
                f"key persisted for the previous one. Set {credential_variable} "
                f"(the Hive OWUI_SHIM_KEY) together with {url_variable}.",
                fatal,
            )
            # Non-fatal path: drop the destination instead of persisting it
            # half-paired, so the stored URL and credential stay the
            # consistent pair the previous boot left behind.
            applied.pop(url_key, None)

    return applied


def log_summary(applied: dict) -> str:
    """One-line, secret-free description of what was reconciled.

    A secret is named without its value. A prompt template is named with its
    length instead of its text: upstream's `rag.template` default alone runs to
    about twenty five lines, so ten of these logged verbatim would push
    kilobytes of prose into the boot log on every start and bury the line an
    operator actually reads it for. A prompt is not automatically safe to print
    either, since an operator may put internal policy text in one and nothing
    marks it the way an api key is marked. Everything else keeps its value,
    because for those the value IS the signal: the embedding model Open WebUI
    will send is the one thing #722 never surfaced anywhere.
    """

    def rendered(key: str) -> str:
        if key in SECRET_KEYS:
            return key
        if key in TEMPLATE_KEYS:
            return f"{key}=<{len(applied[key])} chars>"
        return f"{key}={applied[key]}"

    return ", ".join(rendered(key) for key in sorted(applied))


def permission_overrides(environ) -> dict:
    """The permission leaves the environment explicitly sets, as {path: bool}.

    A missing or blank variable yields no entry, exactly like `overrides`
    above, so an unset variable never silently revokes a permission an
    administrator chose. The parse matches upstream's own
    (`os.getenv(...).lower() == 'true'`), so an unrecognised value means off
    here for the same reason it does there.
    """
    applied = {}
    for path, variable in PERMISSION_ENV.items():
        value = (environ.get(variable) or "").strip()
        if value:
            applied[path] = value.lower() == "true"
    return applied


def merge_permissions(current: dict, overrides_by_path: dict) -> dict:
    """Return a new tree with the named leaves set, siblings untouched.

    A copy rather than an in-place edit, for two reasons. The caller compares
    the result against the stored tree to decide whether to write at all,
    which a mutation would make impossible, and a whole-tree write is what
    reaches the database, so losing a sibling here would silently revoke a
    permission nobody asked to change.

    A branch that is absent, or present but not a dict, is created. An older
    deployment's persisted tree predates whatever key was added since, so
    absence is the normal case rather than corruption.
    """
    merged = copy.deepcopy(current) if isinstance(current, dict) else {}
    for path, value in overrides_by_path.items():
        node = merged
        for key in path[:-1]:
            if not isinstance(node.get(key), dict):
                node[key] = {}
            node = node[key]
        node[path[-1]] = value
    return merged


# CORRECTED post-review (PR #1582 security review, escalated to CRITICAL).
# The original 13-entry version of this allowlist rested on one claim
# ("utils/oauth.py imports every oauth.* name directly from open_webui.config
# as a frozen module constant, never via Config.get"), checked by grepping
# for the literal string `Config.get('oauth.`. That grep has a blind spot:
# `utils/oauth.py` also reads a whole cluster of oauth.* keys through
# `get_oauth_runtime_config()` (line 180), which calls
# `Config.get_many(*keys)` against a dynamically built key list
# (`OAUTH_RUNTIME_CONFIG`, line 121), a call shape no literal-string grep for
# `Config.get(` matches. Eight of the original thirteen entries are read that
# way and were misclassified; five are not and stay here.
#
# Re-audited by grepping the pinned image for each entry's PERSISTED KEY
# STRING (e.g. `'oauth.client_id'`), across the whole backend, not just
# oauth.py or auths.py, since that string is what any consumer has to
# reference regardless of whether it calls `Config.get`, `Config.get_many`
# with a literal list, or `Config.get_many` with a dict-derived one. Per
# entry:
#
# - OAUTH_CLIENT_ID, OAUTH_CLIENT_SECRET, OAUTH_CODE_CHALLENGE_METHOD,
#   OAUTH_PROVIDER_NAME, OAUTH_SCOPES: their persisted keys
#   (`oauth.client_id`, `oauth.client_secret`, `oauth.code_challenge_method`,
#   `oauth.provider_name`, `oauth.scopes`) appear in exactly three places in
#   the whole pinned backend: `config.py`'s DEFAULT_CONFIG (the seed-default
#   assignment itself), the historical `3ff2c63645b8_reshape_config_to_per_
#   key_rows` migration, and `routers/auths.py`'s `OAUTH_CONFIG_KEYS` dict,
#   which backs only the `/admin/config/oauth` GET/POST pair (admin-panel
#   display and edit, and this fork's admin panel is removed from the
#   frontend bundle). No `Config.get`/`Config.get_many` call anywhere in the
#   backend resolves any of these five outside that admin pair, confirmed by
#   grepping every `Config.get_many(` call site in the pinned image (17 of
#   them) and checking each one's key list by hand. `utils/oauth.py` uses the
#   frozen module constants for the actual Authlib client registration
#   (config.py:2600-2632), which re-reads os.environ fresh on every container
#   start regardless of the database.
#
# The remaining eight were moved into RAG_CONFIG_ENV/FEATURE_CONFIG_ENV
# above instead of staying here: `oauth.enable_signup`,
# `oauth.enable_role_mapping`, `oauth.enable_group_mapping`,
# `oauth.roles_claim`, `oauth.group_claim`, `oauth.allowed_roles`,
# `oauth.admin_roles` are read live via `get_oauth_runtime_config()` from
# `handle_login`/`handle_callback`/`get_user_role`/`update_user_groups`
# (utils/oauth.py:1671, 1691, 1403, 1490), and `oauth.provider_url` has a
# separate, lower-traffic live read as a `/signout` fallback
# (routers/auths.py:926). All eight were live-diverged risks the moment this
# module shipped, not only future-drift risk: docker-compose.yml sets seven
# of the eight today (ENABLE_OAUTH_SIGNUP, ENABLE_OAUTH_ROLE_MANAGEMENT,
# ENABLE_OAUTH_GROUP_MANAGEMENT, OAUTH_ROLES_CLAIM, OAUTH_ALLOWED_ROLES,
# OAUTH_ADMIN_ROLES, OAUTH_GROUPS_CLAIM).
#
# `oauth.auto_redirect` is a ninth member of this cluster that was never on
# this list at all: it is genuinely read live (see its own entry in
# FEATURE_CONFIG_ENV above), which is exactly why it needed reconciling from
# the start.
ENVIRONMENT_ONLY_ENV_VARS = frozenset(
    {
        "OAUTH_CLIENT_ID",
        "OAUTH_CLIENT_SECRET",
        "OAUTH_CODE_CHALLENGE_METHOD",
        "OAUTH_PROVIDER_NAME",
        "OAUTH_SCOPES",
    }
)

_ASSIGNMENT_RE = re.compile(r"^([A-Z][A-Z0-9_]*)\s*=")
_ENV_READ_RE = re.compile(r"os\.(?:getenv|environ\.get)\(\s*['\"]([A-Z0-9_]+)['\"]")
_DEFAULT_CONFIG_ENTRY_RE = re.compile(r"'([a-z0-9_.]+)':\s*([A-Z][A-Za-z0-9_]*)\s*,")


def _persisted_config_env_vars(source: str) -> dict:
    """Map env-var-name -> the DEFAULT_CONFIG dotted keys it backs.

    A heuristic block scan, not a real Python parser (ponytail: see the
    ceiling below), built for the #1575 audit and reused here so the guard
    stays right whenever the pinned image changes rather than needing a
    hand-kept list. A block-level scan rather than a per-line regex, because
    several DEFAULT_CONFIG values are built with a multi-line list
    comprehension (OAUTH_ALLOWED_ROLES, OAUTH_ADMIN_ROLES) and a per-line
    match misses both entirely.

    Ceiling: a block that calls os.getenv more than once with different
    variable names attributes all of its DEFAULT_CONFIG keys to whichever
    call the regex finds first, and a constant built with no direct
    os.getenv/os.environ.get call in its own assignment (fully derived from
    another constant, e.g. RAG_OPENAI_API_BASE_URL falling back to
    OPENAI_API_BASE_URL when unset) is missed entirely. Both are
    read-fresh-every-boot false negatives, not false positives: this can
    under-report a gap, never invent one that is not backed by a variable
    this deployment actually sets, so it fails closed toward "stays silent"
    rather than toward "blocks a good boot".
    """
    lines = source.splitlines()
    block_starts = [i for i, line in enumerate(lines) if _ASSIGNMENT_RE.match(line)]
    const_to_env: dict = {}
    for idx, start in enumerate(block_starts):
        end = block_starts[idx + 1] if idx + 1 < len(block_starts) else len(lines)
        name_match = _ASSIGNMENT_RE.match(lines[start])
        env_match = _ENV_READ_RE.search("\n".join(lines[start:end]))
        if name_match and env_match:
            const_to_env.setdefault(name_match.group(1), env_match.group(1))

    default_config_start = source.find("DEFAULT_CONFIG = {")
    if default_config_start == -1:
        return {}
    depth = 0
    default_config_end = default_config_start
    for i, ch in enumerate(source[default_config_start:]):
        if ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                default_config_end = default_config_start + i + 1
                break
    block = source[default_config_start:default_config_end]

    env_to_keys: dict = {}
    for key, const in _DEFAULT_CONFIG_ENTRY_RE.findall(block):
        env = const_to_env.get(const)
        if env:
            env_to_keys.setdefault(env, []).append(key)
    return env_to_keys


def guard_unreconciled_env_vars(
    environ, config_source: str, fatal: bool = True
) -> None:
    """Report, loudly, if the deploy environment sets a variable that backs
    an Open WebUI persisted config key and this module neither reconciles it
    nor lists it on ENVIRONMENT_ONLY_ENV_VARS with a stated reason.

    Issue #1575 acceptance criterion 4. The WEB_LOADER_TIMEOUT bug it was
    filed for was silent by construction: the container environment was
    correct, `docker compose config` was correct, and only reading the
    effective config store on the box showed the disagreement. This turns
    the *next* instance of that same class into a loud finding instead of
    something someone has to notice by hand on a live deployment.

    2026-08-30 postmortem (the incident that produced `fatal`): the original
    #1575 shape always raised, and the first time it actually found something
    -- `TIKTOKEN_ENCODING_NAME` and `WHISPER_MODEL`, both baked into the
    pinned image rather than set by compose -- that raise happened inside
    Open WebUI's FastAPI startup on the live demo box, so it took chat down
    with a crash loop rather than failing a build. The finding was correct;
    aborting a live service over it was not. `fatal=True` (the default) is
    for CI, where a raise is a red build, the proportionate blast radius for
    a config-hygiene gap. Production boot must call this with `fatal=False`:
    same detection, but a finding logs at ERROR and startup continues, so a
    gap discovered for the first time on a running deployment degrades to
    "noticed and fixable" rather than "down." See
    `deploy/docker/owui-patches/apply_rag_env_config_patch.py` for the boot
    call site.

    The same postmortem applies to `overrides`, which `reconcile` calls on the
    very next line of that same boot splice and which carried eight raises of
    this exact shape (PR #1588 review). They route through `_refuse` now, so
    at `fatal=False` a refused value logs at ERROR and leaves its own persisted
    row alone rather than aborting startup. Nothing on this splice aborts a
    boot over configuration any more. A `Config.upsert` database error still
    can, which is an infrastructure failure rather than a hygiene finding, and
    is not this module's to swallow.
    """
    reconciled = (
        set(RAG_CONFIG_ENV.values())
        | set(FEATURE_CONFIG_ENV.values())
        | set(PERMISSION_ENV.values())
        | {UPLOAD_CEILING_ENV, OPENAI_CONNECTION_URL_ENV, OPENAI_CONNECTION_KEY_ENV}
    )
    env_to_keys = _persisted_config_env_vars(config_source)
    gaps = {
        var: keys
        for var, keys in env_to_keys.items()
        if (environ.get(var) or "").strip()
        and var not in reconciled
        and var not in ENVIRONMENT_ONLY_ENV_VARS
    }
    if not gaps:
        return
    details = "; ".join(f"{var} -> {keys}" for var, keys in sorted(gaps.items()))
    message = (
        f"issue #1575 guard: this deployment sets {len(gaps)} "
        f"environment variable(s) that back an Open WebUI persisted "
        f"config key and this module does not reconcile: {details}. "
        f"Either add it to RAG_CONFIG_ENV/FEATURE_CONFIG_ENV/"
        f"PERMISSION_ENV so the environment keeps winning after a first "
        f"boot, or add it to ENVIRONMENT_ONLY_ENV_VARS with a comment "
        f"proving the consuming code never reads it from the Config "
        f"store (see the #1575 audit for the worked examples)."
    )
    if fatal:
        raise RuntimeError(message)
    # ponytail: stdlib logging only, no dependency on Open WebUI's own `log`
    # (the caller already has that one in scope and could pass it in, but a
    # second, independent path to visibility is the point after 2026-08-30:
    # this must still surface even if that logger's own setup is ever what
    # breaks). Python's logging module always has a last-resort stderr
    # handler for WARNING and above, so this is visible in `docker compose
    # logs` with zero configuration on the caller's part.
    _logger.error(message)


async def reconcile(config, environ, fatal: bool = True) -> dict:
    """Overwrite the persisted keys the environment names. Returns them.

    `fatal` is forwarded to `overrides`, so the boot splice can refuse a bad
    value without aborting startup. See `_refuse`.
    """
    applied = overrides(environ, fatal)
    if applied:
        await config.upsert(applied)

    by_path = permission_overrides(environ)
    if by_path:
        current = await config.get(PERMISSIONS_KEY) or {}
        merged = merge_permissions(current, by_path)
        # Compared rather than written unconditionally: this runs on every
        # boot, and rewriting an identical row would make the startup log
        # claim a permission changed on a start where nothing did.
        if merged != current:
            await config.upsert({PERMISSIONS_KEY: merged})
            # Logged as the leaves that moved, not as the whole tree, which is
            # large and mostly unrelated. The row written is still
            # PERMISSIONS_KEY; nothing dotted is ever persisted.
            applied[PERMISSIONS_KEY] = {
                ".".join(path): value for path, value in by_path.items()
            }

    return applied
