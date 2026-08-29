#!/usr/bin/env python3
"""Self-check for the Open WebUI RAG-config reconcile patch (issue #722).

Open WebUI persists every `rag.*` config key in its own database the first
time a container boots and thereafter ignores the environment: its
`Config.seed_defaults` only inserts keys that do not already exist
("Existing DB values take precedence over defaults"). The demo box therefore
kept sending `text-embedding-3-small`, the value `docker-compose.yml` shipped
between 2026-05-18 and 2026-07-26, long after that default was corrected to a
Hive catalog alias, and every document upload failed alias resolution at the
gateway with a 404.

deploy/docker/owui-patches/hive_rag_env_config.py is spliced into that startup
path so the environment wins for exactly these keys. This file exercises it
directly: no framework, no network, no Open WebUI import.
Run: python3 scripts/test_owui_rag_env_config.py
"""
import asyncio
import importlib.util
import re
import sys
from pathlib import Path

MODULE_PATH = (
    Path(__file__).resolve().parents[1]
    / "deploy"
    / "docker"
    / "owui-patches"
    / "hive_rag_env_config.py"
)
spec = importlib.util.spec_from_file_location("hive_rag_env_config", MODULE_PATH)
hive_rag_env_config = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hive_rag_env_config)

ALIAS = "hive-embedding-default"
STALE = "text-embedding-3-small"


class FakeConfig:
    """Stand-in for open_webui.models.config.Config, whose upsert overwrites
    the persisted row for every key handed to it."""

    def __init__(self, stored: dict):
        self.stored = dict(stored)
        self.upsert_calls = 0

    async def get(self, key: str, default=None):
        return self.stored.get(key, default)

    async def upsert(self, updates: dict) -> None:
        self.upsert_calls += 1
        self.stored.update(updates)


def reconcile(config, environ):
    return asyncio.run(hive_rag_env_config.reconcile(config, environ))


def test_env_overrides_a_stale_persisted_model() -> None:
    """The #722 failure itself: a persisted row naming a model the Hive catalog
    does not expose, and an environment naming the alias it does."""
    config = FakeConfig({"rag.embedding_model": STALE, "rag.embedding_engine": "openai"})
    applied = reconcile(
        config,
        {
            "RAG_EMBEDDING_ENGINE": "openai",
            "RAG_EMBEDDING_MODEL": ALIAS,
            "RAG_OPENAI_API_BASE_URL": "http://edge-api:8080/v1",
            "RAG_OPENAI_API_KEY": "hk_example",
        },
    )
    # The resolved model itself, not merely the absence of an error.
    assert config.stored["rag.embedding_model"] == ALIAS, config.stored
    assert applied["rag.embedding_model"] == ALIAS, applied
    assert config.stored["rag.openai.api_base_url"] == "http://edge-api:8080/v1", config.stored
    assert config.stored["rag.openai.api_key"] == "hk_example", config.stored
    assert config.upsert_calls == 1


def test_alias_is_taken_from_the_environment_not_a_literal() -> None:
    """D-001: the embedding model is admin-selectable, so whatever
    OWUI_RAG_EMBEDDING_ALIAS names is what gets pushed. Nothing is pinned."""
    config = FakeConfig({"rag.embedding_model": STALE})
    reconcile(config, {"RAG_EMBEDDING_MODEL": "hive-embedding-bge-m3-1024"})
    assert config.stored["rag.embedding_model"] == "hive-embedding-bge-m3-1024", config.stored


def test_unset_and_blank_env_leave_the_persisted_value_alone() -> None:
    """A deployment that does not configure RAG through the gateway keeps
    whatever its admin chose in Open WebUI's own settings."""
    for environ in ({}, {"RAG_EMBEDDING_MODEL": "   "}):
        config = FakeConfig({"rag.embedding_model": "sentence-transformers/all-MiniLM-L6-v2"})
        applied = reconcile(config, environ)
        assert applied == {}, applied
        assert config.upsert_calls == 0
        assert config.stored["rag.embedding_model"] == "sentence-transformers/all-MiniLM-L6-v2"


def test_base_url_without_a_key_is_refused() -> None:
    """A credential must not outlive the destination it was issued for.

    reconcile only writes the keys the environment supplies, so a base URL
    supplied on its own would repoint Open WebUI's embedder while it kept
    sending the API key persisted for the previous destination. Refuse loudly
    instead, naming both variables. Dropping the base-URL override quietly is
    not an option: silence is the failure mode this whole change exists to end.
    """
    for key_value in (None, "", "   "):
        environ = {
            "RAG_EMBEDDING_MODEL": ALIAS,
            "RAG_OPENAI_API_BASE_URL": "http://somewhere-else:8080/v1",
        }
        if key_value is not None:
            environ["RAG_OPENAI_API_KEY"] = key_value

        config = FakeConfig(
            {
                "rag.openai.api_base_url": "http://edge-api:8080/v1",
                "rag.openai.api_key": "hk_issued_for_edge_api",
            }
        )
        try:
            reconcile(config, environ)
        except RuntimeError as exc:
            message = str(exc)
        else:
            raise AssertionError(f"expected a refusal for key_value={key_value!r}")

        assert "RAG_OPENAI_API_BASE_URL" in message, message
        assert "RAG_OPENAI_API_KEY" in message, message
        # The refusal must not print the credential it is protecting.
        assert "hk_issued_for_edge_api" not in message, message
        # Nothing is written, so the persisted pair stays internally consistent.
        assert config.upsert_calls == 0
        assert config.stored["rag.openai.api_base_url"] == "http://edge-api:8080/v1"
        assert config.stored["rag.openai.api_key"] == "hk_issued_for_edge_api"


def test_key_without_a_base_url_is_allowed() -> None:
    """The reverse pairing is safe and is how shim-key rotation reaches Open
    WebUI: a new credential for the destination already persisted."""
    config = FakeConfig(
        {"rag.openai.api_base_url": "http://edge-api:8080/v1", "rag.openai.api_key": "hk_old"}
    )
    applied = reconcile(config, {"RAG_OPENAI_API_KEY": "hk_new"})
    assert applied == {"rag.openai.api_key": "hk_new"}, applied
    assert config.stored["rag.openai.api_key"] == "hk_new"
    assert config.stored["rag.openai.api_base_url"] == "http://edge-api:8080/v1"


def test_only_the_gateway_rag_keys_are_touched() -> None:
    """Nothing else an admin configured through Open WebUI is overwritten:
    this reconcile is deliberately not `ENABLE_PERSISTENT_CONFIG=false`."""
    config = FakeConfig({"ui.enable_signup": False, "rag.top_k": 5})
    applied = reconcile(
        config, {"RAG_EMBEDDING_MODEL": ALIAS, "RAG_TOP_K": "42", "ENABLE_SIGNUP": "true"}
    )
    assert set(applied) == {"rag.embedding_model"}, applied
    assert config.stored["ui.enable_signup"] is False
    assert config.stored["rag.top_k"] == 5


def test_values_are_stripped() -> None:
    config = FakeConfig({})
    reconcile(config, {"RAG_EMBEDDING_MODEL": f"  {ALIAS}\n"})
    assert config.stored["rag.embedding_model"] == ALIAS, config.stored


def test_login_form_is_reconciled_as_a_json_boolean() -> None:
    """The SSO-only login fix. `ui.enable_login_form` reaches the browser raw
    as features.enable_login_form and the login page tests it for truthiness,
    so the persisted value has to be a real boolean: the string "false" is
    truthy in JavaScript and would render the dead credential form anyway."""
    config = FakeConfig({"ui.enable_login_form": True})
    applied = reconcile(config, {"ENABLE_LOGIN_FORM": "false"})
    assert applied["ui.enable_login_form"] is False, applied
    assert config.stored["ui.enable_login_form"] is False, config.stored


def test_login_form_stays_enabled_when_the_environment_says_true() -> None:
    """A deployment that wants the password form keeps it, and the coercion
    matches Open WebUI's own (`os.getenv(...).lower() == 'true'`), so a
    reconciled value is identical to one a first boot would have seeded."""
    for value, expected in (("true", True), ("True", True), ("0", False), ("no", False)):
        config = FakeConfig({})
        reconcile(config, {"ENABLE_LOGIN_FORM": value})
        assert config.stored["ui.enable_login_form"] is expected, (value, config.stored)


def test_unset_login_form_leaves_the_persisted_value_alone() -> None:
    """Same posture as the RAG keys: an unset variable never clobbers."""
    config = FakeConfig({"ui.enable_login_form": True})
    applied = reconcile(config, {})
    assert applied == {}, applied
    assert config.stored["ui.enable_login_form"] is True


def test_hybrid_search_is_reconciled_as_a_json_boolean() -> None:
    """Issue #832. Open WebUI's hybrid retrieval path builds a RerankCompressor
    for every query, and with no reranking model configured that compressor
    re-embeds the query and every retrieved document to score them, so one
    question costs three gateway embedding round trips instead of one. On the
    demo box that took longer than Caddy's 60s response_header_timeout and
    every knowledge-backed answer came back 504. The flag lives in Open WebUI's
    persisted config, so it is subject to the same first-boot-wins trap as the
    keys above and has to be reconciled rather than merely set in compose."""
    config = FakeConfig({"rag.enable_hybrid_search": True})
    applied = reconcile(config, {"ENABLE_RAG_HYBRID_SEARCH": "false"})
    assert applied["rag.enable_hybrid_search"] is False, applied
    assert config.stored["rag.enable_hybrid_search"] is False, config.stored


def test_hybrid_search_can_be_turned_back_on() -> None:
    """A deployment that configures a real reranking model should get hybrid
    retrieval back, because then the compressor calls the reranker instead of
    re-embedding and the extra recall is worth one call."""
    config = FakeConfig({"rag.enable_hybrid_search": False})
    reconcile(config, {"ENABLE_RAG_HYBRID_SEARCH": "true"})
    assert config.stored["rag.enable_hybrid_search"] is True, config.stored


def test_compose_leaves_hybrid_search_off_by_default() -> None:
    """The reconcile only helps if compose actually names a value, and the
    default has to be off: the whole failure was a persisted `true` that no
    compose edit could reach. Asserted against the file rather than trusted,
    because this is one line away from silently reinstating the 504."""
    compose = (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text(encoding="utf-8")
    assert (
        "ENABLE_RAG_HYBRID_SEARCH: ${OWUI_RAG_HYBRID_SEARCH:-false}" in compose
    ), "docker-compose.yml must default Open WebUI hybrid retrieval to off (issue #832)"


def test_sso_auto_redirect_is_reconciled_as_a_json_boolean() -> None:
    """The sign in page offered a single control, "Continue with Hive", which is
    a choice between one option. `oauth.auto_redirect` sends the visitor
    straight to the provider instead. It is in Open WebUI's DEFAULT_CONFIG, so
    the demo box seeded it false on its first boot and compose alone could never
    move it, the same first-boot trap as every key above."""
    config = FakeConfig({"oauth.auto_redirect": False})
    applied = reconcile(config, {"OAUTH_AUTO_REDIRECT": "true"})
    assert applied["oauth.auto_redirect"] is True, applied
    assert config.stored["oauth.auto_redirect"] is True, config.stored


def test_sso_auto_redirect_can_be_turned_back_off() -> None:
    """A deployment that adds a second provider, or re-enables the password
    form, needs the picker back. The page checks those conditions itself at
    runtime, and this is the deployment-wide off switch in front of them."""
    config = FakeConfig({"oauth.auto_redirect": True})
    reconcile(config, {"OAUTH_AUTO_REDIRECT": "false"})
    assert config.stored["oauth.auto_redirect"] is False, config.stored


def test_compose_turns_sso_auto_redirect_on() -> None:
    """The reconcile only helps if compose names a value. Asserted against the
    file because the whole point of the change is that a visitor never sees the
    intermediate page."""
    compose = (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text(encoding="utf-8")
    assert (
        'OAUTH_AUTO_REDIRECT: "true"' in compose
    ), "docker-compose.yml must enable the single-provider sign in redirect"


def test_speech_to_text_is_pointed_at_the_gateway() -> None:
    """The Bengali dictation failure. Open WebUI ships `audio.stt.engine` as ""
    (its "use my own bundled Whisper" value) and seeds it on first boot, so the
    chat microphone transcribed inside the container with WHISPER_MODEL=base and
    never reached the Hive gateway. base returns romanized Latin for Bengali at
    any clip length, and forcing a language hint does not change that; the same
    audio through hive-stt (groq/whisper-large-v3) returns Bengali script."""
    config = FakeConfig(
        {
            "audio.stt.engine": "",
            "audio.stt.model": "",
            "audio.stt.openai.api_base_url": "https://api.openai.com/v1",
            "audio.stt.openai.api_key": "",
        }
    )
    applied = reconcile(
        config,
        {
            "AUDIO_STT_ENGINE": "openai",
            "AUDIO_STT_MODEL": "hive-stt",
            "AUDIO_STT_OPENAI_API_BASE_URL": "http://edge-api:8080/v1",
            "AUDIO_STT_OPENAI_API_KEY": "hk_example",
        },
    )
    assert config.stored["audio.stt.engine"] == "openai", config.stored
    assert config.stored["audio.stt.model"] == "hive-stt", config.stored
    assert config.stored["audio.stt.openai.api_base_url"] == "http://edge-api:8080/v1", config.stored
    assert config.stored["audio.stt.openai.api_key"] == "hk_example", config.stored
    assert applied["audio.stt.engine"] == "openai", applied


def test_stt_base_url_without_a_key_is_refused() -> None:
    """Same rule as the RAG pair: a credential must not outlive the destination
    it was issued for. Only the supplied keys are written, so a base URL on its
    own would repoint transcription while Open WebUI kept sending the key
    persisted for the previous destination."""
    for key_value in (None, "", "   "):
        environ = {
            "AUDIO_STT_ENGINE": "openai",
            "AUDIO_STT_OPENAI_API_BASE_URL": "http://somewhere-else:8080/v1",
        }
        if key_value is not None:
            environ["AUDIO_STT_OPENAI_API_KEY"] = key_value

        config = FakeConfig(
            {
                "audio.stt.openai.api_base_url": "http://edge-api:8080/v1",
                "audio.stt.openai.api_key": "hk_issued_for_edge_api",
            }
        )
        try:
            reconcile(config, environ)
        except RuntimeError as exc:
            message = str(exc)
        else:
            raise AssertionError(f"expected a refusal for key_value={key_value!r}")

        assert "AUDIO_STT_OPENAI_API_BASE_URL" in message, message
        assert "AUDIO_STT_OPENAI_API_KEY" in message, message
        assert "hk_issued_for_edge_api" not in message, message
        assert config.upsert_calls == 0
        assert config.stored["audio.stt.openai.api_base_url"] == "http://edge-api:8080/v1"
        assert config.stored["audio.stt.openai.api_key"] == "hk_issued_for_edge_api"


def test_unset_stt_env_leaves_the_persisted_engine_alone() -> None:
    """An Enterprise box running the sovereign `voice` profile (Parakeet plus
    faster-whisper) configures Open WebUI's speech-to-text itself. An unset
    variable must never clobber that back to the gateway."""
    config = FakeConfig({"audio.stt.engine": "openai", "audio.stt.model": "sovereign-stt"})
    applied = reconcile(config, {"AUDIO_STT_ENGINE": "  "})
    assert applied == {}, applied
    assert config.upsert_calls == 0
    assert config.stored["audio.stt.model"] == "sovereign-stt"


def test_no_deployment_wide_speech_language_is_configured() -> None:
    """Measured, same speaker and recording: whisper-large-v3 auto-detects
    Bengali correctly from 5 seconds up, and an explicit hint rescues shorter
    clips. But a forced deployment-wide language is not the fix, because English
    speech transcribed with language=bn comes back as Bengali garbage, and Open
    WebUI's WHISPER_LANGUAGE overrides every user's own setting rather than
    filling in for it. The language stays per user (Settings > Audio), which the
    composer already sends and the gateway already forwards verbatim."""
    compose = (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text(encoding="utf-8")
    # A YAML assignment, not the word: the compose comment names the variable to
    # explain why it is not used, and that comment is the point.
    assigned = re.search(r"^\s*WHISPER_LANGUAGE\s*:", compose, re.MULTILINE)
    assert assigned is None, (
        "WHISPER_LANGUAGE forces one language on every user of the deployment "
        "and would trade broken Bengali for broken English"
    )


def test_text_to_speech_is_pointed_at_the_gateway() -> None:
    """Issue #997. Open WebUI ships audio.tts.engine as "" (its bundled browser
    speech synthesis), api_base_url api.openai.com, an empty key, model tts-1
    and voice alloy, and seeds all five on first boot. The gateway's hive-tts
    alias accepts none of those: alloy is rejected upstream (#996) and
    api.openai.com is not served by anyone, so read-aloud was dead."""
    config = FakeConfig(
        {
            "audio.tts.engine": "",
            "audio.tts.model": "tts-1",
            "audio.tts.voice": "alloy",
            "audio.tts.openai.api_base_url": "https://api.openai.com/v1",
            "audio.tts.openai.api_key": "",
        }
    )
    applied = reconcile(
        config,
        {
            "AUDIO_TTS_ENGINE": "openai",
            "AUDIO_TTS_MODEL": "hive-tts",
            "AUDIO_TTS_VOICE": "autumn",
            "AUDIO_TTS_OPENAI_API_BASE_URL": "http://edge-api:8080/v1",
            "AUDIO_TTS_OPENAI_API_KEY": "hk_example",
        },
    )
    assert config.stored["audio.tts.engine"] == "openai", config.stored
    assert config.stored["audio.tts.model"] == "hive-tts", config.stored
    assert config.stored["audio.tts.voice"] == "autumn", config.stored
    assert config.stored["audio.tts.openai.api_base_url"] == "http://edge-api:8080/v1", config.stored
    assert config.stored["audio.tts.openai.api_key"] == "hk_example", config.stored
    assert applied["audio.tts.voice"] == "autumn", applied


def test_tts_base_url_without_a_key_is_refused() -> None:
    """Same rule as the RAG and STT pairs: a credential must not outlive the
    destination it was issued for. Only the supplied keys are written, so a
    base URL on its own would repoint read-aloud while Open WebUI kept sending
    the key persisted for the previous destination."""
    for key_value in (None, "", "   "):
        environ = {
            "AUDIO_TTS_ENGINE": "openai",
            "AUDIO_TTS_OPENAI_API_BASE_URL": "http://somewhere-else:8080/v1",
        }
        if key_value is not None:
            environ["AUDIO_TTS_OPENAI_API_KEY"] = key_value

        config = FakeConfig(
            {
                "audio.tts.openai.api_base_url": "http://edge-api:8080/v1",
                "audio.tts.openai.api_key": "hk_issued_for_edge_api",
            }
        )
        try:
            reconcile(config, environ)
        except RuntimeError as exc:
            message = str(exc)
        else:
            raise AssertionError(f"expected a refusal for key_value={key_value!r}")

        assert "AUDIO_TTS_OPENAI_API_BASE_URL" in message, message
        assert "AUDIO_TTS_OPENAI_API_KEY" in message, message
        assert "hk_issued_for_edge_api" not in message, message
        assert config.upsert_calls == 0
        assert config.stored["audio.tts.openai.api_base_url"] == "http://edge-api:8080/v1"
        assert config.stored["audio.tts.openai.api_key"] == "hk_issued_for_edge_api"


def test_unset_tts_env_leaves_the_persisted_voice_alone() -> None:
    """An Enterprise box running the sovereign `voice` profile configures Open
    WebUI's text-to-speech itself. An unset variable must never clobber that
    back to the gateway."""
    config = FakeConfig({"audio.tts.engine": "openai", "audio.tts.model": "sovereign-tts", "audio.tts.voice": "daniel"})
    applied = reconcile(config, {"AUDIO_TTS_ENGINE": "  "})
    assert applied == {}, applied
    assert config.upsert_calls == 0
    assert config.stored["audio.tts.model"] == "sovereign-tts"
    assert config.stored["audio.tts.voice"] == "daniel"


def test_compose_routes_chat_read_aloud_through_the_gateway() -> None:
    """The reconcile only helps if compose names the values. Asserted against
    the file because the whole defect (#997) was an unset variable set letting
    upstream's own defaults win by omission, and the voice default has to name
    a voice the provider actually has (#996)."""
    compose = (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text(encoding="utf-8")
    for line in (
        'AUDIO_TTS_ENGINE: "openai"',
        'AUDIO_TTS_OPENAI_API_BASE_URL: "http://edge-api:8080/v1"',
        "AUDIO_TTS_OPENAI_API_KEY: ${OWUI_SHIM_KEY:-}",
        "AUDIO_TTS_MODEL: ${OWUI_TTS_ALIAS:-hive-tts}",
        "AUDIO_TTS_VOICE: ${OWUI_TTS_VOICE:-autumn}",
    ):
        assert line in compose, f"docker-compose.yml must set {line}"


def test_gateway_serves_the_voice_roster_the_ui_offers() -> None:
    """Issue #996 via the UI. Open WebUI's get_available_voices, for an openai
    engine on a non-OpenAI base URL, fetches GET {base_url}/audio/voices and
    falls back to Open WebUI's hardcoded alloy-style list when that fetch
    fails. edge-api now serves that endpoint with the provider's real roster,
    so the Settings > Audio dropdowns can only offer voices hive-tts accepts.
    Asserted against main.go because a dropped registration line would send
    every dropdown silently back to the alloy fallback."""
    main_go = (
        Path(__file__).resolve().parents[1]
        / "apps" / "edge-api" / "cmd" / "server" / "main.go"
    ).read_text(encoding="utf-8")
    assert 'mux.Handle("/v1/audio/voices", audio.VoicesHandler())' in main_go, (
        "edge-api must serve GET /v1/audio/voices or Open WebUI's voice "
        "dropdowns fall back to OpenAI's alloy-style list (#996)"
    )


def test_compose_routes_chat_transcription_through_the_gateway() -> None:
    """The reconcile only helps if compose names the values. Asserted against
    the file because the whole defect was an unset variable letting upstream's
    own default win by omission."""
    compose = (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text(encoding="utf-8")
    for line in (
        'AUDIO_STT_ENGINE: "openai"',
        'AUDIO_STT_OPENAI_API_BASE_URL: "http://edge-api:8080/v1"',
        "AUDIO_STT_OPENAI_API_KEY: ${OWUI_SHIM_KEY:-}",
        "AUDIO_STT_MODEL: ${OWUI_STT_ALIAS:-hive-stt}",
    ):
        assert line in compose, f"docker-compose.yml must set {line}"


def test_env_example_does_not_enable_the_stt_sidecars_by_default() -> None:
    """`handleTranscription` hands the whole request to the Parakeet plus
    faster-whisper sidecars as soon as either URL is set, and stops consulting
    the catalog route. Those sidecars only run under the `voice` compose
    profile, so an .env copied from the example onto a `local` or `chat` stack
    would point chat dictation at hosts that are not running, defeating the fix
    above. They stay commented out until an operator opts in."""
    env_example = (Path(__file__).resolve().parents[1] / ".env.example").read_text(
        encoding="utf-8"
    )
    for variable in ("PARAKEET_BASE_URL", "FASTER_WHISPER_BASE_URL"):
        assigned = re.search(rf"^{variable}=", env_example, re.MULTILINE)
        assert assigned is None, (
            f"{variable} must stay commented out in .env.example: setting it "
            "takes /v1/audio/transcriptions away from the catalog route and "
            "hands it to a sidecar that only exists under the `voice` profile"
        )


def test_reconciled_keys_are_loggable_without_the_secret() -> None:
    """The startup log line names the model (the signal this investigation
    lacked twice) and never the API key value."""
    summary = hive_rag_env_config.log_summary(
        {
            "rag.embedding_model": ALIAS,
            "rag.openai.api_key": "hk_secret",
            "audio.stt.model": "hive-stt",
            "audio.stt.openai.api_key": "hk_secret",
            "audio.tts.model": "hive-tts",
            "audio.tts.openai.api_key": "hk_secret",
        }
    )
    assert ALIAS in summary, summary
    assert "hk_secret" not in summary, summary
    assert "rag.openai.api_key" in summary, summary
    # Same for the transcription and read-aloud pairs: the alias is the
    # signal, the key is not.
    assert "hive-stt" in summary, summary
    assert "audio.stt.openai.api_key" in summary, summary
    assert "hive-tts" in summary, summary
    assert "audio.tts.openai.api_key" in summary, summary


# --------------------------------------------------------------------------
# The permission tree, which is ONE persisted row and therefore cannot ride
# the flat dictionaries above.
#
# `user.permissions` holds the whole nested permission tree in a single config
# row (open_webui.config.DEFAULT_CONFIG). Writing a row keyed
# "user.permissions.workspace.skills" would succeed, persist, and be read by
# nothing at all: `utils/access_control.has_permission` walks the tree inside
# the one row. That silent no-op is the reason these have their own seam.

PERMISSIONS_KEY = "user.permissions"


def _tree(**workspace) -> dict:
    """A permission tree shaped like Open WebUI's own, trimmed to what these
    tests read. Siblings are present so a merge that drops them fails."""
    base = {"models": False, "knowledge": True, "prompts": False, "skills": False, "tools": False}
    base.update(workspace)
    return {
        "workspace": base,
        "sharing": {"public_skills": False},
        "chat": {"controls": True, "system_prompt": True},
    }


def test_skills_permission_is_read_from_the_environment() -> None:
    """The variable is Open WebUI's own name for this permission, so an
    operator setting it gets the behaviour its documentation implies."""
    assert hive_rag_env_config.permission_overrides(
        {"USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS": "true"}
    ) == {("workspace", "skills"): True}
    assert hive_rag_env_config.permission_overrides(
        {"USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS": "false"}
    ) == {("workspace", "skills"): False}


def test_unset_or_blank_permission_env_yields_nothing() -> None:
    """Same posture as every other key here: an unset variable never clobbers
    a persisted value, so a deployment that chose its own permissions keeps
    them."""
    assert hive_rag_env_config.permission_overrides({}) == {}
    assert hive_rag_env_config.permission_overrides(
        {"USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS": "   "}
    ) == {}


def test_merge_sets_the_leaf_without_dropping_its_siblings() -> None:
    """A whole-tree write is what reaches the database, so a merge that loses
    a sibling silently revokes a permission nobody asked to change."""
    before = _tree()
    after = hive_rag_env_config.merge_permissions(before, {("workspace", "skills"): True})

    assert after["workspace"]["skills"] is True
    assert after["workspace"]["knowledge"] is True
    assert after["workspace"]["models"] is False
    assert after["sharing"] == {"public_skills": False}
    assert after["chat"] == {"controls": True, "system_prompt": True}


def test_merge_does_not_mutate_the_tree_it_was_given() -> None:
    """Ledger-style immutability, and it is load bearing here: the caller
    compares the merged tree against the stored one to decide whether to write
    at all, which an in-place mutation would make impossible."""
    before = _tree()
    hive_rag_env_config.merge_permissions(before, {("workspace", "skills"): True})
    assert before["workspace"]["skills"] is False


def test_merge_creates_a_missing_branch() -> None:
    """An older deployment's persisted tree predates a permission key, so the
    branch may be absent rather than false."""
    after = hive_rag_env_config.merge_permissions({}, {("workspace", "skills"): True})
    assert after == {"workspace": {"skills": True}}


def test_reconcile_writes_the_whole_permission_tree_back() -> None:
    """The #722 trap applied to permissions: the row was seeded on first boot
    with skills false and has outranked the environment ever since."""
    config = FakeConfig({PERMISSIONS_KEY: _tree()})
    reconcile(config, {"USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS": "true"})

    assert config.stored[PERMISSIONS_KEY]["workspace"]["skills"] is True
    assert config.stored[PERMISSIONS_KEY]["workspace"]["knowledge"] is True


def test_reconcile_never_writes_a_flat_dotted_permission_row() -> None:
    """The whole reason this seam is separate. A row nothing reads is worse
    than no row, because the deployment then looks configured."""
    config = FakeConfig({PERMISSIONS_KEY: _tree()})
    reconcile(config, {"USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS": "true"})

    assert "user.permissions.workspace.skills" not in config.stored


def test_reconcile_leaves_permissions_alone_when_the_env_is_unset() -> None:
    config = FakeConfig({PERMISSIONS_KEY: _tree(skills=True)})
    reconcile(config, {})
    assert config.stored[PERMISSIONS_KEY]["workspace"]["skills"] is True


def test_reconcile_skips_the_write_when_the_tree_already_agrees() -> None:
    """No pointless row rewrite on every boot, and it proves the comparison is
    against the merged tree rather than against the override alone."""
    config = FakeConfig({PERMISSIONS_KEY: _tree(skills=True)})
    reconcile(config, {"USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS": "true"})
    assert config.upsert_calls == 0


def test_compose_grants_the_skills_permission() -> None:
    """The deployment's own answer, read from the file that reaches the box.
    A reconcile that works and a compose file that never sets the variable is
    the shape that passes every unit test and changes nothing in production."""
    compose = (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text()
    assert re.search(
        r"USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS:\s*\"?true\"?", compose
    ), "docker-compose.yml does not grant workspace.skills"


# --------------------------------------------------------------------------
# Chat upload limits (issue #1405).
#
# Measured live 2026-08-29: a 28.6 MB attachment produced a composer chip and a
# POST that had not returned after 105 seconds, with no progress, no timeout and
# no error, and a Windows executable uploaded and processed cleanly. Open WebUI
# already enforces both, in the pinned image's own
# `routers/files.py.upload_file_handler`: an extension outside
# `rag.file.allowed_extensions` is refused with 400 before the bytes reach
# storage, and a file over `rag.file.max_size` megabytes is refused with 413 and
# the stored object deleted. Both were simply unset, because the compose
# variable was never in the reconcile map and so was the #722 silent no-op.


def test_the_chat_cap_is_derived_from_the_ingest_ceiling() -> None:
    """One settable ceiling for the whole product (issue #1428).

    The cap must persist as a JSON number, not a string. Open WebUI publishes
    this row straight to the browser as `file.max_size` and
    `MessageInput.svelte` computes `max_size * 1024 * 1024` from it, so a
    string happens to work in JavaScript and is still wrong: the row a first
    boot would have seeded is an int (upstream parses the variable with
    `int()`), and a type that differs from the seeded one is how the boolean
    keys above went wrong before."""
    config = FakeConfig({})
    applied = reconcile(config, {"RAG_MAX_UPLOAD_BYTES": "26214400"})
    assert applied["rag.file.max_size"] == 25, applied
    assert isinstance(applied["rag.file.max_size"], int), applied
    assert config.stored["rag.file.max_size"] == 25, config.stored


def test_the_derived_chat_cap_rounds_down() -> None:
    """The chat surface must never accept what the ingest path refuses, so a
    byte value that is not a whole number of megabytes rounds toward the
    smaller cap. Rounding up would reopen the divergence one megabyte at a
    time, in the direction that hurts: the composer would admit a file
    `edge-api` and the markitdown sidecar would both reject."""
    applied = hive_rag_env_config.overrides({"RAG_MAX_UPLOAD_BYTES": "27000000"})
    assert applied["rag.file.max_size"] == 25, applied


def test_a_second_independent_chat_cap_is_refused() -> None:
    """`RAG_FILE_MAX_SIZE` is what produced issue #1428: it was a second,
    independently settable ceiling for the same user action, in different
    units, and the box's `.env` set it to 100 while the ingest path enforced
    25. Compose no longer passes it, so it should never appear in this
    container; if it ever does again, it must fail the boot rather than
    quietly win or quietly lose."""
    try:
        hive_rag_env_config.overrides(
            {"RAG_MAX_UPLOAD_BYTES": "26214400", "RAG_FILE_MAX_SIZE": "100"}
        )
    except RuntimeError as error:
        assert "RAG_FILE_MAX_SIZE" in str(error), error
        assert "RAG_MAX_UPLOAD_BYTES" in str(error), error
    else:
        raise AssertionError("a second, independent chat upload cap was accepted")


def test_upload_type_allowlist_is_reconciled_as_a_list() -> None:
    """The load-bearing coercion. `upload_file_handler` evaluates
    `file_extension not in allowed_file_extensions`, so persisting the raw
    comma string would turn a membership test into a SUBSTRING test: `df` would
    pass because `pdf` contains it, and any extension that happens to be a
    substring of an allowed one would be admitted. Upstream's own parse of the
    variable produces a list, and so must this."""
    config = FakeConfig({})
    applied = reconcile(config, {"RAG_ALLOWED_FILE_EXTENSIONS": "pdf, txt ,MD"})
    assert applied["rag.file.allowed_extensions"] == ["pdf", "txt", "md"], applied
    assert config.stored["rag.file.allowed_extensions"] == ["pdf", "txt", "md"]


def test_a_malformed_size_cap_is_refused_rather_than_ignored() -> None:
    """A cap that cannot be parsed must fail the container at startup naming the
    variable, not boot with no cap. Silently dropping it is the exact failure
    this module exists to end: the deployment would look configured and would
    accept a 30 MB upload anyway."""
    try:
        hive_rag_env_config.overrides({"RAG_MAX_UPLOAD_BYTES": "25MB"})
    except RuntimeError as error:
        assert "RAG_MAX_UPLOAD_BYTES" in str(error), error
    else:
        raise AssertionError("a non-numeric RAG_MAX_UPLOAD_BYTES was accepted")


def test_a_sub_megabyte_ceiling_is_refused() -> None:
    """Zero, and anything that floors to zero, is not merely a useless cap: it
    is a cap the two consumers read oppositely. Open WebUI's server-side check
    is `if max_size and len(contents) > ...`, where 0 is falsy and enforces
    nothing, while the browser's is `file.size > max_size * 1024 * 1024`, where
    0 rejects every file of non-zero length. A deployment there would refuse
    every upload in the composer while leaving the API accepting files of
    unlimited size, and would read as a working cap."""
    for value in ("0", "1", "1048575"):
        try:
            hive_rag_env_config.overrides({"RAG_MAX_UPLOAD_BYTES": value})
        except RuntimeError as error:
            assert "RAG_MAX_UPLOAD_BYTES" in str(error), error
        else:
            raise AssertionError(f"RAG_MAX_UPLOAD_BYTES={value} was accepted")


def test_a_unicode_digit_ceiling_is_refused_not_crashed_on() -> None:
    """`str.isdigit()` is true for characters `int()` then refuses, so a
    ceiling of "²⁵" would leave this module raising ValueError out of a
    function whose contract is RuntimeError, and the operator would get a
    traceback naming neither the variable nor what to do. "٣" is the other
    half: `int()` accepts it, so without an ASCII check it would silently
    configure a three megabyte ceiling that no grep for a number would find."""
    for value in ("²⁵", "٣٠"):
        try:
            hive_rag_env_config.overrides({"RAG_MAX_UPLOAD_BYTES": value})
        except RuntimeError as error:
            assert "RAG_MAX_UPLOAD_BYTES" in str(error), error
        else:
            raise AssertionError(f"RAG_MAX_UPLOAD_BYTES={value!r} was accepted")


def test_allowlist_entries_written_with_a_leading_dot_still_match() -> None:
    """`.pdf,.txt` is the obvious thing to write, and upstream compares against
    an extension it has already stripped the dot from, so persisting the dot
    would produce a list that matches nothing and refuses every upload while
    the deployment looks correctly configured. That is the same failure shape as
    the substring trap above, reached from the other direction."""
    config = FakeConfig({})
    applied = reconcile(config, {"RAG_ALLOWED_FILE_EXTENSIONS": ".PDF, .txt , md"})
    assert applied["rag.file.allowed_extensions"] == ["pdf", "txt", "md"], applied


def test_an_allowlist_of_only_separators_is_refused() -> None:
    """An empty list is falsy in upstream's `if process and
    allowed_file_extensions`, so persisting one turns the type check off while
    the deployment's own configuration says it is on."""
    for value in (",", " . , ", ",,,"):
        try:
            hive_rag_env_config.overrides({"RAG_ALLOWED_FILE_EXTENSIONS": value})
        except RuntimeError as error:
            assert "RAG_ALLOWED_FILE_EXTENSIONS" in str(error), error
        else:
            raise AssertionError(f"{value!r} was accepted as an allowlist")


def test_unset_upload_limits_leave_the_persisted_values_alone() -> None:
    """Same contract as every other key here: an unset or blank variable writes
    nothing, so an administrator's own choice survives, and an enterprise
    deployment that never sets these is not silently capped."""
    config = FakeConfig({"rag.file.max_size": 50, "rag.file.allowed_extensions": ["pdf"]})
    applied = reconcile(
        config, {"RAG_MAX_UPLOAD_BYTES": "", "RAG_ALLOWED_FILE_EXTENSIONS": "  "}
    )
    assert "rag.file.max_size" not in applied, applied
    assert "rag.file.allowed_extensions" not in applied, applied
    assert config.stored["rag.file.max_size"] == 50, config.stored
    assert config.stored["rag.file.allowed_extensions"] == ["pdf"], config.stored


def _compose_text() -> str:
    return (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text(encoding="utf-8")


def _compose_allowed_extensions() -> list:
    """The allowlist docker-compose.yml actually hands the container."""
    match = re.search(
        r"RAG_ALLOWED_FILE_EXTENSIONS:\s*\$\{RAG_ALLOWED_FILE_EXTENSIONS:-([^}]*)\}",
        _compose_text(),
    )
    assert match, "docker-compose.yml does not set RAG_ALLOWED_FILE_EXTENSIONS"
    return [ext.strip() for ext in match.group(1).split(",") if ext.strip()]


def test_one_expression_sets_every_upload_ceiling() -> None:
    """The structural half of the fix for issue #1428, and the assertion that
    has to be able to go red.

    Before this, two services held two independently settable ceilings for one
    user action, in two different units: `RAG_MAX_UPLOAD_BYTES` in bytes on
    edge-api and the markitdown sidecar, `RAG_FILE_MAX_SIZE` in whole megabytes
    on the chat surface. Nothing made them agree, so PR #1426 could set the
    chat one to 25 in compose and still be defeated by a `RAG_FILE_MAX_SIZE=100`
    line in the deployment's own `.env`, because an explicit value beats a
    compose fallback. That is not a number that needed correcting once more; it
    is a mechanism.

    So: one expression, interpolated identically into all three services, and
    no second knob anywhere in the file. Re-introducing one fails here."""
    compose = _compose_text()
    # Naming the retired variable in a comment is wanted: it is how the next
    # reader learns why the division exists. Setting it is what must fail, so
    # match the two shapes that would, an environment key and an interpolation,
    # rather than the bare word.
    for shape in ("RAG_FILE_MAX_SIZE:", "${RAG_FILE_MAX_SIZE"):
        assert shape not in compose, (
            f"docker-compose.yml sets {shape!r}: a second, independently settable "
            "chat upload cap is what issue #1428 is. The chat cap is derived from "
            "RAG_MAX_UPLOAD_BYTES by deploy/docker/owui-patches/hive_rag_env_config.py"
        )
    # Matched without pinning the value, so raising the product's document
    # ceiling stays a one-line edit in three places rather than four. What must
    # not change is that the three agree: a test that pinned 26214400 would
    # fail on a deliberate raise, which trains the next person to edit the test
    # until it passes, and that is how a guard stops guarding.
    defaults = re.findall(r"\$\{RAG_MAX_UPLOAD_BYTES:-([^}]*)\}", compose)
    assert len(defaults) == 3, (
        f"expected the one ceiling expression on exactly three services "
        f"(edge-api, the markitdown sidecar, open-webui), found {len(defaults)}"
    )
    assert len(set(defaults)) == 1, (
        f"the three services disagree about the default upload ceiling: "
        f"{defaults}. They must interpolate one identical expression, or a "
        f"deployment that sets nothing gets a different limit in chat than on "
        f"its own ingest path, which is issue #1428"
    )


def test_compose_allowlist_refuses_executables() -> None:
    """The reported defect: `evil.exe` uploaded and was processed. An allowlist
    is the only mechanism upstream offers, and the final `else` in
    `Loader._get_loader` falls back to TextLoader for ANY unrecognised
    extension, so without one every binary is 'processed' into mojibake and
    stored."""
    allowed = _compose_allowed_extensions()
    for refused in ("exe", "zip", "dll", "bin", "so", "msi", "apk", "jar", "iso", "dmg"):
        assert refused not in allowed, f"chat uploads must not accept .{refused}"
    assert allowed, "an empty allowlist disables the check entirely upstream"


def test_compose_allowlist_covers_every_format_this_deployment_can_read() -> None:
    """The allowlist is derived by a rule, not by taste: everything Open WebUI
    can turn into text. A cap that refuses a file the product could have read is
    worse than no cap, because it fails in front of a user rather than in a log.

    The rule is `known_source_ext` plus the extensions `Loader._get_loader`
    names in its own branches. Asserted against the vendored source so an
    upstream bump that adds a format fails here rather than silently starting to
    refuse it."""
    loaders = (
        Path(__file__).resolve().parents[1]
        / "vendor"
        / "open-webui"
        / "backend"
        / "open_webui"
        / "retrieval"
        / "loaders"
        / "main.py"
    ).read_text(encoding="utf-8")
    block = re.search(r"known_source_ext = \[(.*?)\]", loaders, re.S)
    assert block, "known_source_ext moved; the allowlist derivation needs revisiting"
    known = set(re.findall(r"['\"]([^'\"]+)['\"]", block.group(1)))
    # Without this the test cannot fail: an upstream reformat that the regex
    # stops matching yields an empty set, an empty set is a subset of anything,
    # and the assertion below then reports full coverage over nothing.
    assert len(known) > 20, (
        f"only {len(known)} extensions were extracted from known_source_ext; "
        "the vendored source was reformatted and this derivation is now blind"
    )
    documents = {
        "pdf", "csv", "rst", "xml", "htm", "html", "md", "docx", "doc",
        "xls", "xlsx", "ppt", "pptx", "msg", "odt", "epub", "txt",
    }
    allowed = set(_compose_allowed_extensions())
    missing = sorted((known | documents) - allowed)
    assert not missing, f"chat uploads would refuse formats the product can read: {missing}"


def test_env_example_documents_the_upload_limits() -> None:
    """Both variables are the operator's lever if a real document is refused
    mid demo, so an operator has to be able to find them without reading
    compose."""
    env_example = (Path(__file__).resolve().parents[1] / ".env.example").read_text(
        encoding="utf-8"
    )
    for variable in ("RAG_MAX_UPLOAD_BYTES", "RAG_ALLOWED_FILE_EXTENSIONS"):
        assert variable in env_example, f".env.example does not document {variable}"
    assert "RAG_FILE_MAX_SIZE=" not in env_example, (
        ".env.example must not offer RAG_FILE_MAX_SIZE as a settable knob: an "
        "operator who copies it into a real .env re-creates issue #1428"
    )


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui RAG env-config reconcile (issue #722)")


if __name__ == "__main__":
    sys.exit(main())
