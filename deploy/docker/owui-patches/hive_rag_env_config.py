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
point Open WebUI's embedder at the Hive gateway, plus `ui.enable_login_form`
(see its entry below, same mechanism, different symptom) and the three
product-surface feature flags below (#772), and for nothing else: an
administrator's other Open WebUI settings still persist normally, which is
why this is a per-key reconcile rather than `ENABLE_PERSISTENT_CONFIG=false`.

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

# Open WebUI persisted config key -> the environment variable that owns it.
# Key names are Open WebUI's own (open_webui.config.DEFAULT_CONFIG); the
# variable names are the ones docker-compose.yml sets on the open-webui
# service.
RAG_CONFIG_ENV = {
    "rag.embedding_engine": "RAG_EMBEDDING_ENGINE",
    "rag.embedding_model": "RAG_EMBEDDING_MODEL",
    "rag.openai.api_base_url": "RAG_OPENAI_API_BASE_URL",
    "rag.openai.api_key": "RAG_OPENAI_API_KEY",
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
    "rag.enable_hybrid_search": "ENABLE_RAG_HYBRID_SEARCH",
}

# Keys Open WebUI stores as a JSON boolean rather than a string. The value has
# to be coerced on the way in: `features.enable_login_form` is published raw to
# the browser, and the login page tests it for truthiness, so a persisted
# string "false" is truthy there and would render the very form this is meant
# to remove. Open WebUI's own parse is `os.getenv(...).lower() == 'true'`
# (config.py:1617), and this matches it so the reconciled value is identical to
# the one a first boot would have seeded.
BOOLEAN_KEYS = frozenset({"ui.enable_login_form"})

# Keys whose value must never be logged. The rest are named with their value,
# because the embedding model Open WebUI will actually send is the one signal
# this failure mode never produced anywhere: Open WebUI logs only aiohttp's
# bare "404, message='Not Found'" for it, having discarded the response body
# that names the model.
SECRET_KEYS = frozenset({"rag.openai.api_key"})


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
        value = (environ.get(variable) or "").strip()
        if value:
            applied[key] = value.lower() == "true" if key in BOOLEAN_KEYS else value

    for key, variable in FEATURE_CONFIG_ENV.items():
        value = (environ.get(variable) or "").strip()
        if value:
            # Upstream's own parse of these variables
            # (open_webui.config: os.getenv(name, 'True').lower() == 'true'),
            # so an unrecognised value means off here exactly as it does there.
            applied[key] = value.lower() == "true"

    if "rag.openai.api_base_url" in applied and "rag.openai.api_key" not in applied:
        raise RuntimeError(
            "RAG_OPENAI_API_BASE_URL is set to "
            f"{applied['rag.openai.api_base_url']!r} but RAG_OPENAI_API_KEY is "
            "empty or unset. Refusing to point Open WebUI's embedder at that "
            "destination while it keeps sending the API key persisted for the "
            "previous one. Set RAG_OPENAI_API_KEY (the Hive OWUI_SHIM_KEY) "
            "together with RAG_OPENAI_API_BASE_URL."
        )

    return applied


def log_summary(applied: dict) -> str:
    """One-line, secret-free description of what was reconciled."""
    return ", ".join(
        key if key in SECRET_KEYS else f"{key}={applied[key]}" for key in sorted(applied)
    )


async def reconcile(config, environ) -> dict:
    """Overwrite the persisted keys the environment names. Returns them."""
    applied = overrides(environ)
    if applied:
        await config.upsert(applied)
    return applied
