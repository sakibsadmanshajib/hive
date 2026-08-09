"""Keep Hive's non-chat catalog aliases out of Open WebUI's chat model picker
(issues #772, #792).

The gateway serves one model list for every client, and it must keep doing
that: `GET /v1/models` on the API origin is an OpenAI-contract surface that
`packages/openai-contract/matrix/support-matrix.json` marks supported, and
upstream OpenAI lists its embedding and audio model ids there too. Direct API
clients must keep seeing `hive-embedding-default`, `hive-stt` and `hive-tts`.
The chat picker is the only surface where listing them is wrong, because none
of the three serves chat completions and picking one produces a dead
conversation.

So the filter belongs on the listing the picker reads and nowhere else.
`/api/models` in Open WebUI's `main.py` is exactly that listing: it is built
from `request.app.state.MODELS`, but it does not write it back, so removing an
entry here removes it from the dropdown while leaving every invocation path
untouched. Nothing in Open WebUI resolves a model for a chat completion, a RAG
embedding, or a text-to-speech call through this response.

Why not Open WebUI's own per-model access control, which is what #776 tried:
`deploy/docker/docker-compose.yml` sets `BYPASS_MODEL_ACCESS_CONTROL: "true"`
by deliberate design (the gateway is the single source of truth for model
visibility, D-014), and in the pinned v0.10.2 image that flag makes
`main.py` skip `get_filtered_models` entirely, for every role. Even with the
flag off, `get_filtered_models` exempts administrators whenever
`BYPASS_ADMIN_ACCESS_CONTROL` is set, and that variable defaults to true
(`config.py:2029`) while this deployment promotes every tenant owner to an
Open WebUI administrator. Measured on a booted v0.10.2 container: with the
flag off, an administrator still saw all six aliases and a member saw an
empty picker and got HTTP 400 "Model not found" on every chat request. So an
access-control-shaped fix cannot hide anything from the people who use the
product, and turning the flag off to reach it breaks chat for everyone else.

This filter is therefore unconditional. It reads no access-control flag, no
role, and no group membership, because every one of those is either bypassed
or admin-exempt on this deployment.

The hidden set is never hardcoded here. It is the union of

  * `HIVE_PICKER_HIDDEN_MODEL_IDS`, a comma-separated list compose owns, and
  * whichever aliases Open WebUI is itself configured to call for a non-chat
    purpose (`RAG_EMBEDDING_MODEL`, `AUDIO_TTS_MODEL`, `AUDIO_STT_MODEL`).

The second half matters because the RAG embedding alias is admin-selectable
and drives vector-store provisioning (D-001), so a deployment that changes it
must not have to remember to edit a second list to keep the picker clean.
An unset or blank variable contributes nothing, so a deployment that sets none
of them keeps upstream behaviour exactly.
"""

# Comma-separated list of model ids compose wants out of the chat picker.
HIDDEN_IDS_ENV = "HIVE_PICKER_HIDDEN_MODEL_IDS"

# Open WebUI's own settings for the three non-chat modalities. Anything named
# here is by definition not a chat model, so it is hidden without needing a
# second mention in HIDDEN_IDS_ENV.
MODALITY_MODEL_ENV = (
    "RAG_EMBEDDING_MODEL",
    "AUDIO_TTS_MODEL",
    "AUDIO_STT_MODEL",
)


def hidden_model_ids(environ) -> frozenset:
    """Model ids the chat picker must not list, from the environment alone."""
    hidden = set()

    for entry in (environ.get(HIDDEN_IDS_ENV) or "").split(","):
        entry = entry.strip()
        if entry:
            hidden.add(entry)

    for variable in MODALITY_MODEL_ENV:
        value = (environ.get(variable) or "").strip()
        if value:
            hidden.add(value)

    return frozenset(hidden)


def filter_models(models, environ):
    """Drop the hidden ids from a `/api/models` list.

    Returns the input unchanged when nothing is configured, so this is a no-op
    on a deployment that sets none of the variables above. Entries without an
    `id` are passed through rather than dropped: this filter exists to remove
    three known aliases, not to police the shape of upstream's payload.
    """
    hidden = hidden_model_ids(environ)
    if not hidden:
        return models
    return [model for model in models if model.get("id") not in hidden]
