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
    # Chat upload limits (#1405), and the same first-boot trap for the third
    # time, with the compose half already written. `RAG_FILE_MAX_SIZE` has been
    # on the open-webui service since the #1108 follow-up, carrying a comment
    # saying it serves `rag.file.max_size` to clients so the composer's size
    # guard fires client-side. It never reached anything: the key was not in
    # this map, so nothing reconciled it, and the row a first boot seeded from
    # `DEFAULT_CONFIG` (None, meaning no limit) has outranked the variable ever
    # since. Measured live on the deployed chat 2026-08-29: a 28.6 MB
    # attachment produced a composer chip and a POST that had still not
    # returned after 105 seconds, with no progress, no timeout and no error,
    # and a Windows executable uploaded and was processed with a 200.
    #
    # Neither value is enforced here. Both are enforced by the pinned image's
    # own `routers/files.py.upload_file_handler`, read out of the running
    # container rather than assumed: an extension outside
    # `rag.file.allowed_extensions` is refused with 400 before the bytes reach
    # storage, and a file over `rag.file.max_size` megabytes is refused with
    # 413 and the stored object deleted. Open WebUI also publishes the size cap
    # through `/api/config` as `file.max_size`, which is what lets
    # MessageInput.svelte refuse an oversized file before the request is made.
    # Client-side is the fast, legible refusal; the server is the enforcement.
    #
    # These two are types, not strings, and both coercions matter. See
    # INTEGER_KEYS and LIST_KEYS below.
    "rag.file.max_size": "RAG_FILE_MAX_SIZE",
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

# Keys Open WebUI stores as a JSON number. `rag.file.max_size` is published raw
# to the browser as `file.max_size`, where MessageInput.svelte computes
# `max_size * 1024 * 1024`, and is read back server-side as
# `int(max_size) * 1024 * 1024`. A string survives both by coincidence, and is
# still wrong: upstream parses the variable with `int()`, so a first boot seeds
# a number, and a reconciled value whose type differs from the seeded one is
# how `ui.enable_login_form` went wrong before.
INTEGER_KEYS = frozenset({"rag.file.max_size"})

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
    }
)

# Keys whose value must never be logged. The rest are named with their value,
# because the embedding model Open WebUI will actually send is the one signal
# this failure mode never produced anywhere: Open WebUI logs only aiohttp's
# bare "404, message='Not Found'" for it, having discarded the response body
# that names the model.
SECRET_KEYS = frozenset(
    {"rag.openai.api_key", "audio.stt.openai.api_key", "audio.tts.openai.api_key"}
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


def overrides(environ) -> dict:
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
    applied = {}
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
        elif key in INTEGER_KEYS:
            # Refused rather than dropped. A cap that cannot be parsed and is
            # quietly ignored leaves the deployment looking configured while it
            # goes on accepting a 30 MB upload, which is the silent-no-op
            # failure this whole module exists to end. Failing here surfaces as
            # a startup failure naming the variable.
            #
            # Zero is refused for a sharper reason than "it is not a useful
            # cap". The two consumers disagree about what it means. Open
            # WebUI's server-side check is `if max_size and len(contents) >
            # ...`, where 0 is falsy, so the backend enforces nothing at all.
            # The browser's is `file.size > max_size * 1024 * 1024`, where 0
            # rejects every file of non-zero length. A deployment set to 0 would
            # therefore refuse every upload in the composer while leaving the
            # API accepting files of unlimited size, which is worse than either
            # end of the range and would read as "the cap works".
            if not value.isdigit() or int(value) == 0:
                raise RuntimeError(
                    f"{variable} must be a whole number of megabytes greater "
                    f"than zero, got {value!r}. Zero disables the server-side "
                    f"cap while making the composer refuse every file, and a "
                    f"value that cannot be parsed would leave the deployment "
                    f"looking capped while enforcing nothing."
                )
            applied[key] = int(value)
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
                raise RuntimeError(
                    f"{variable} is set to {value!r}, which parses to no "
                    f"extensions at all. An empty allowlist turns the type "
                    f"check off entirely while the deployment still looks "
                    f"configured. Name the extensions to allow. Removing the "
                    f"allowlist altogether is a change to the compose default, "
                    f"made deliberately, not something an empty value does."
                )
            applied[key] = items
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
            raise RuntimeError(
                f"{url_variable} is set to {applied[url_key]!r} but "
                f"{credential_variable} is empty or unset. Refusing to point "
                f"{consumer} at that destination while it keeps sending the API "
                f"key persisted for the previous one. Set {credential_variable} "
                f"(the Hive OWUI_SHIM_KEY) together with {url_variable}."
            )

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


async def reconcile(config, environ) -> dict:
    """Overwrite the persisted keys the environment names. Returns them."""
    applied = overrides(environ)
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
