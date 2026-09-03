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
import ast
import asyncio
import importlib.util
import logging
import re
import sys
import textwrap
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

PATCH_PATH = (
    Path(__file__).resolve().parents[1]
    / "deploy"
    / "docker"
    / "owui-patches"
    / "apply_rag_env_config_patch.py"
)
GATEWAY_URL = "http://edge-api:8080/v1"
SEARX_URL = "http://searxng:8080/search"


class _ErrorCapture(logging.Handler):
    """Collects the module logger's messages, so a non-fatal refusal can be
    asserted to have actually been reported rather than swallowed."""

    def __init__(self) -> None:
        super().__init__()
        self.messages: list[str] = []

    def emit(self, record: logging.LogRecord) -> None:
        self.messages.append(record.getMessage())


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
    this reconcile is deliberately not `ENABLE_PERSISTENT_CONFIG=false`.

    Issue #1575 moved `RAG_TOP_K`/`ENABLE_SIGNUP` from "unrelated, must stay
    untouched" to "reconciled, on purpose" (both are read live from the
    Config store: retrieval's `get_retrieval_config()` for the former, the
    signup gate in routers/auths.py for the latter), so this now proves the
    negative with `OAUTH_CLIENT_ID` instead: it backs a persisted config key
    too, but `utils/oauth.py` reads it as a frozen module constant, never via
    `Config.get`, so it stays environment-only by design (see
    ENVIRONMENT_ONLY_ENV_VARS). RAG_TOP_K and ENABLE_SIGNUP get their own
    assertions in the same test, since their presence here is the whole
    point of the change."""
    config = FakeConfig({"oauth.client_id": "persisted-client-id", "rag.top_k": 5})
    applied = reconcile(
        config,
        {
            "RAG_EMBEDDING_MODEL": ALIAS,
            "RAG_TOP_K": "42",
            "ENABLE_SIGNUP": "true",
            "OAUTH_CLIENT_ID": "env-client-id",
        },
    )
    assert set(applied) == {"rag.embedding_model", "rag.top_k", "ui.enable_signup"}, applied
    assert config.stored["oauth.client_id"] == "persisted-client-id"
    assert applied["rag.top_k"] == 42 and isinstance(applied["rag.top_k"], int)
    assert applied["ui.enable_signup"] is True


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


def test_knowledge_permission_is_read_from_the_environment() -> None:
    """Issue #1505. `workspace.knowledge` gates exactly one route in the pinned
    image, `POST /api/v1/knowledge/create` (routers/knowledge.py), and upstream
    defaults it false. Listing, reading, file add and delete are all scoped by
    ownership or an access grant instead, so an ordinary customer could see and
    open a knowledge base and could never author one: the Projects page's New
    project button answered 401 for every non-admin account."""
    assert hive_rag_env_config.permission_overrides(
        {"USER_PERMISSIONS_WORKSPACE_KNOWLEDGE_ACCESS": "true"}
    ) == {("workspace", "knowledge"): True}
    assert hive_rag_env_config.permission_overrides(
        {"USER_PERMISSIONS_WORKSPACE_KNOWLEDGE_ACCESS": "false"}
    ) == {("workspace", "knowledge"): False}


def test_reconcile_grants_knowledge_without_disturbing_skills() -> None:
    """Both leaves live in the same single row, so the second one added has to
    merge rather than replace. A tree written from one override alone would
    revoke the other permission on the next boot."""
    config = FakeConfig({PERMISSIONS_KEY: _tree(knowledge=False, skills=True)})
    reconcile(
        config,
        {
            "USER_PERMISSIONS_WORKSPACE_KNOWLEDGE_ACCESS": "true",
            "USER_PERMISSIONS_WORKSPACE_SKILLS_ACCESS": "true",
        },
    )

    assert config.stored[PERMISSIONS_KEY]["workspace"]["knowledge"] is True
    assert config.stored[PERMISSIONS_KEY]["workspace"]["skills"] is True
    assert config.stored[PERMISSIONS_KEY]["workspace"]["models"] is False


def test_compose_grants_the_knowledge_permission() -> None:
    """Same reason the skills assertion below exists: a reconcile that works
    and a compose file that never sets the variable changes nothing on the
    box."""
    compose = (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text()
    assert re.search(
        r"USER_PERMISSIONS_WORKSPACE_KNOWLEDGE_ACCESS:\s*\"?true\"?", compose
    ), "docker-compose.yml does not grant workspace.knowledge"


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


def _compose_service_block(compose: str, service: str) -> str:
    """The text of one compose service, from its own key to the next one.

    Service keys sit at exactly two spaces of indentation under `services:`,
    and nothing else in this file does, so that is the boundary. Returning the
    slice rather than searching the whole document is what lets a caller assert
    something about one service instead of about the file, which is the
    difference between a guard and a word count.
    """
    match = re.search(
        rf"^  {re.escape(service)}:$(.*?)(?=^  [a-z0-9][a-z0-9._-]*:$|\Z)",
        compose,
        re.S | re.M,
    )
    assert match, f"docker-compose.yml has no service named {service!r}"
    return match.group(1)


def test_compose_service_block_isolates_one_service() -> None:
    """The slicing helper above is load bearing for the guard below it, so it
    gets its own check. Without this, a regex that silently matched the whole
    document would make that guard pass over anything at all."""
    compose = _compose_text()
    edge = _compose_service_block(compose, "edge-api")
    markitdown = _compose_service_block(compose, "markitdown")
    assert "MARKITDOWN_URL" in edge, "edge-api's own block was not returned"
    assert "MARKITDOWN_URL" not in markitdown, (
        "the slice for markitdown leaked into another service's block, so every "
        "per-service assertion built on it is meaningless"
    )
    assert "hive-markitdown:ci" in markitdown, "markitdown's own block was not returned"
    assert "hive-markitdown:ci" not in edge, "the slice for edge-api leaked"


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
    # Checked per service rather than by counting occurrences across the whole
    # file. A bare count of three is blind to a swap: drop the expression from
    # open-webui and duplicate it inside edge-api and the count still reads
    # three while the property this test claims to hold is false.
    #
    # Matched without pinning the value, so raising the product's document
    # ceiling stays a one-line edit in three places rather than four. What must
    # not change is that the three agree: a test that pinned 26214400 would
    # fail on a deliberate raise, which trains the next person to edit the test
    # until it passes, and that is how a guard stops guarding.
    defaults = {}
    for service in ("edge-api", "markitdown", "open-webui"):
        block = _compose_service_block(compose, service)
        found = re.findall(r"\$\{RAG_MAX_UPLOAD_BYTES:-([^}]*)\}", block)
        assert len(found) == 1, (
            f"the {service} service must interpolate the one upload ceiling "
            f"exactly once, found {len(found)}: {found}"
        )
        defaults[service] = found[0]

    assert len(set(defaults.values())) == 1, (
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


# --------------------------------------------------------------------------
# Prompt templates: the task family and the chat-surface RAG template.
#
# These are the prompts Open WebUI itself sends to a model, separately from
# whatever the user typed: the chat title, the tag list, the follow-up
# suggestions, the retrieval and web-search query it writes for itself, the
# prompt-based tool-calling preamble, and the instruction wrapped around
# retrieved documents. Every one of them is a persisted-config row with no
# editor anywhere on this deployment. Open WebUI's admin panel is deleted from
# the fork's source and 404'd at the proxy, and every write verb under
# /api/v1/configs is denied there too, so before this the only way to change
# one was a direct SQLite write inside the box's owui-data volume.
#
# The same first-boot-wins trap as #722, confirmed against the demo box's own
# database on 2026-08-29 rather than assumed: `rag.template` carries the full
# upstream default text and all nine `*.prompt_template` rows carry "", every
# one of them written at first boot. So an environment variable alone was a
# silent no-op there, exactly as RAG_EMBEDDING_MODEL was in #722 and
# RAG_FILE_MAX_SIZE was in #1405.
#
# The empty string is not "no template". Each consumer substitutes its own
# DEFAULT_*_PROMPT_TEMPLATE when the persisted value is falsy, so the shipped
# text lives in Python and the row is an override slot. `rag.template` is the
# exception: its default IS the full text, seeded verbatim.


PROMPT_TEMPLATE_KEYS = {
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


def test_every_prompt_template_follows_the_environment() -> None:
    """The whole family, each through its own variable, over a persisted row
    that already exists. One assertion per key rather than a spot check,
    because a map entry with the wrong variable name on either side is exactly
    the mistake that produces a knob nobody can turn."""
    stored = {key: "" for key in PROMPT_TEMPLATE_KEYS}
    config = FakeConfig(stored)
    environ = {
        variable: f"### Task:\nHIVEPROOF for {key}\n"
        for key, variable in PROMPT_TEMPLATE_KEYS.items()
    }
    applied = reconcile(config, environ)
    for key, variable in PROMPT_TEMPLATE_KEYS.items():
        expected = f"### Task:\nHIVEPROOF for {key}\n"
        assert applied[key] == expected, f"{variable} did not reach {key}: {applied}"
        assert config.stored[key] == expected, config.stored[key]


def test_the_prompt_template_variable_names_are_upstreams_own() -> None:
    """Asserted against the vendored config.py, not against this file's own
    table. An operator reading Open WebUI's documentation looks for the name
    upstream reads; inventing a Hive-specific spelling would mean the
    documented variable silently does nothing, which is the failure this whole
    module exists to end. This also catches an upstream rename on a digest
    bump."""
    config_py = (
        Path(__file__).resolve().parents[1]
        / "vendor"
        / "open-webui"
        / "backend"
        / "open_webui"
        / "config.py"
    ).read_text(encoding="utf-8")
    for key, variable in PROMPT_TEMPLATE_KEYS.items():
        assert re.search(
            rf"^\s*'{re.escape(key)}':", config_py, re.MULTILINE
        ), f"{key} is no longer a DEFAULT_CONFIG key upstream"
        assert re.search(
            rf"os\.getenv\(\s*'{re.escape(variable)}'", config_py
        ), f"upstream no longer reads {variable}"


def test_unset_prompt_template_env_leaves_the_persisted_value_alone() -> None:
    """A deployment that sets none of them keeps today's behaviour byte for
    byte. A system prompt reaches every user of a surface, so a default that
    drifted here would be a product-wide behaviour change nobody asked for."""
    stored = {key: f"chosen by an administrator: {key}" for key in PROMPT_TEMPLATE_KEYS}
    config = FakeConfig(dict(stored))
    applied = reconcile(config, {})
    for key in PROMPT_TEMPLATE_KEYS:
        assert key not in applied, applied
        assert config.stored[key] == stored[key], config.stored[key]


def test_a_blank_prompt_template_does_not_erase_the_persisted_one() -> None:
    """Blank is "I am not setting this", not "set it to empty". Same rule as
    every other key here, and it matters more for these: an empty
    `rag.template` row is falsy, so Open WebUI would fall back to its own
    default and the deployment would look configured while sending upstream's
    prompt."""
    config = FakeConfig({"rag.template": "custom grounding text"})
    reconcile(config, {"RAG_TEMPLATE": "   \n  "})
    assert config.stored["rag.template"] == "custom grounding text", config.stored


def test_prompt_template_whitespace_is_preserved() -> None:
    """Every other key in this module is stripped on the way in. A prompt is
    not a model id: its leading indentation and its trailing newline are part
    of what the model receives, and upstream's own read is a bare os.getenv
    with no strip at all, so stripping here would persist a value that differs
    from the one a first boot would have seeded."""
    applied = hive_rag_env_config.overrides(
        {"TITLE_GENERATION_PROMPT_TEMPLATE": "  ### Task:\nGo.\n\n"}
    )
    assert applied["task.title.prompt_template"] == "  ### Task:\nGo.\n\n", applied


def test_prompt_templates_persist_as_strings() -> None:
    """The type a first boot seeds. Open WebUI reads these rows straight into
    a template renderer, so a bool or an int coerced in from one of the other
    frozensets would blow up at request time rather than at boot."""
    applied = hive_rag_env_config.overrides({"RAG_TEMPLATE": "true"})
    assert applied["rag.template"] == "true", applied
    assert isinstance(applied["rag.template"], str), applied


def test_compose_passes_prompt_templates_through_without_defining_them() -> None:
    """The knob ships off, and the shape of "off" is load bearing here.

    Compose's null form (`VAR:` with no value) resolves from the environment
    and, when the variable is set nowhere, leaves it OUT of the container
    entirely. The `${VAR:-}` form does not: it always defines the variable,
    with the empty string. Measured both ways against a running container
    rather than assumed, and both still resolve a real value from --env-file
    and from the shell, which are the two paths an operator actually uses.

    That difference is the whole of `test_an_always_present_rag_template_would_
    break_upstreams_own_default` below. It applies to only one of the ten
    today, and the null form is used for all ten anyway, because the same
    upstream default could be added to any of the others on a digest bump and
    nothing would fail if the difference were left to chance."""
    compose = _compose_text()
    for variable in PROMPT_TEMPLATE_KEYS.values():
        assert re.search(
            rf"^      {variable}:$", compose, re.MULTILINE
        ), f"docker-compose.yml must pass {variable} through in compose's null form"
        assert (
            f"{variable}: ${{{variable}:-}}" not in compose
        ), (
            f"{variable} uses the ${{VAR:-}} form, which defines it as the empty "
            f"string in the container even when nobody set it"
        )


def test_an_always_present_rag_template_would_break_upstreams_own_default() -> None:
    """The one key of the ten whose upstream default is not the empty string,
    and the reason the test above insists on the null form.

    `RAG_TEMPLATE = os.getenv('RAG_TEMPLATE', DEFAULT_RAG_TEMPLATE)`, and
    os.getenv returns its default only when the key is ABSENT: present and
    empty yields ''. So defining the variable unconditionally would make
    DEFAULT_CONFIG['rag.template'] evaluate to '' at import, and a FRESH
    volume's seed_defaults would persist that blank row instead of upstream's
    real default text. The reconcile cannot repair it either, because blank
    means unset there, so it never touches the row.

    Nothing breaks at the model today, because `rag_template()` substitutes
    DEFAULT_RAG_TEMPLATE for a blank value at request time. That masking is
    exactly why this needs a test: the defect would be invisible in behaviour
    while the persisted row silently diverged from what a first boot should
    have written, and the next reader of that row without the same fallback
    (a restored admin panel, an export or diff tool, a migration) would be the
    one to find it.

    Asserted against the vendored source, so a digest bump that gives any of
    the other nine a non-empty default fails here instead of quietly widening
    the hazard."""
    config_py = (
        Path(__file__).resolve().parents[1]
        / "vendor"
        / "open-webui"
        / "backend"
        / "open_webui"
        / "config.py"
    ).read_text(encoding="utf-8")
    with_real_defaults = []
    for variable in PROMPT_TEMPLATE_KEYS.values():
        match = re.search(
            rf"^{re.escape(variable)} = os\.getenv\(\s*'{re.escape(variable)}',\s*([^)]+)\)",
            config_py,
            re.MULTILINE,
        )
        assert match, f"upstream no longer reads {variable} in the expected shape"
        if match.group(1).strip() not in ("''", '""'):
            with_real_defaults.append(variable)
    assert with_real_defaults == ["RAG_TEMPLATE"], (
        "the set of prompt variables whose upstream default is not the empty "
        f"string changed to {with_real_defaults}. Every one of them seeds a "
        "real default that an always-present empty environment variable would "
        "silently replace with a blank row on a fresh volume."
    )


# The prompts this deployment has DELIBERATELY chosen wording for, each with
# the decision that chose it. Adding a name here is the deliberate act; the
# test below then stops treating its presence in the deploy workflow as an
# accident, and goes on refusing every other one.
#
# `rag.template`: issue #1596, and the reason it is first is that upstream's
# own text carries no untrusted-data framing at all. Issue #1571 is the report
# that an unauthenticated third party can get their own page text into this
# template by publishing a page the web loader then fetches, so the framing is
# a security posture and not a style preference. Its POSITION is deliberately
# left at upstream's default: docker-compose.yml holds RAG_SYSTEM_CONTEXT false,
# because upstream renders these rules and the retrieved text into one string
# and promoting one would promote the other. The trusted framing reaches system
# authority through HIVE_CHAT_SYSTEM_PROMPT instead.
DELIBERATELY_SET_FOR_THE_DEMO_BOX = {"RAG_TEMPLATE"}


def test_no_prompt_template_default_is_baked_into_the_repo() -> None:
    """The inverse of the test above, and the one that fails if someone later
    helpfully seeds a Hive prompt into compose or into the deploy workflow's
    env. Changing what every user of a surface is told is a product decision,
    not a side effect of adding a knob.

    Narrowed rather than deleted when the first such decision was actually made
    (issue #1596). Compose stays absolute: that file is shared with the
    enterprise profile and local dev, so no prompt belongs in it at any time.
    The workflow half now allows exactly the names in
    DELIBERATELY_SET_FOR_THE_DEMO_BOX above, so choosing a prompt is an edit to
    that set with a reason next to it, and every other one of the ten still
    fails here."""
    compose = _compose_text()
    workflow = (
        Path(__file__).resolve().parents[1]
        / ".github"
        / "workflows"
        / "deploy-demo-box.yml"
    ).read_text(encoding="utf-8")
    assert DELIBERATELY_SET_FOR_THE_DEMO_BOX <= set(PROMPT_TEMPLATE_KEYS.values()), (
        "DELIBERATELY_SET_FOR_THE_DEMO_BOX names something that is not one of "
        "the ten prompt variables, so this test would be exempting nothing"
    )
    for variable in PROMPT_TEMPLATE_KEYS.values():
        assert not re.search(
            rf"^\s*{variable}:\s*['\"]", compose, re.MULTILINE
        ), f"{variable} carries a literal default in docker-compose.yml"
        if variable in DELIBERATELY_SET_FOR_THE_DEMO_BOX:
            # The exemption is not a free pass: the value still has to be
            # there, as a block scalar, or the deployment is running upstream's
            # text while this file claims a decision was made.
            assert re.search(
                rf"^  {variable}: \|$", workflow, re.MULTILINE
            ), (
                f"{variable} is listed as deliberately set for the demo box but "
                f"the workflow does not set it as a block scalar"
            )
            continue
        assert (
            variable not in workflow
        ), f"{variable} is set for the demo box; that is a product decision, not a knob"


def test_prompt_templates_are_logged_by_size_not_by_value() -> None:
    """The startup line names every reconciled key with its value, which is
    right for a model id and wrong for ten prompt templates: upstream's
    `rag.template` default alone is about twenty five lines, so logging these
    verbatim would push kilobytes of prose into the boot log on every single
    start and bury the one line an operator reads it for. A prompt is also
    not automatically safe to print, since an operator may put internal policy
    text in one, and unlike the api keys above nothing here marks it as such.

    Named with a length rather than dropped, so the log still answers the only
    question it is read for, which is whether the key was reconciled at all."""
    summary = hive_rag_env_config.log_summary(
        {
            "rag.template": "x" * 312,
            "rag.embedding_model": "hive-embedding-default",
        }
    )
    assert "rag.template=<312 chars>" in summary, summary
    assert "x" * 20 not in summary, "the template's text was logged verbatim"
    # The keys that are not prompts must be unaffected: their value IS the
    # signal, which is the whole reason this module logs values at all.
    assert "rag.embedding_model=hive-embedding-default" in summary, summary


def test_env_example_documents_every_prompt_template() -> None:
    """An operator changing a prompt has no UI to do it in on this deployment,
    so .env.example is where they have to find the lever. A knob nobody can
    find is the same as no knob."""
    env_example = (Path(__file__).resolve().parents[1] / ".env.example").read_text(
        encoding="utf-8"
    )
    for variable in PROMPT_TEMPLATE_KEYS.values():
        assert variable in env_example, f".env.example does not document {variable}"


###############################################################################
# Issue #1575: the audit that found WEB_LOADER_TIMEOUT silently inert also
# found thirteen more instances of the same bug, plus a guard against the
# next one. Everything below this line is that.
###############################################################################

VENDOR_CONFIG_PATH = (
    Path(__file__).resolve().parents[1]
    / "vendor"
    / "open-webui"
    / "backend"
    / "open_webui"
    / "config.py"
)


def test_web_loader_timeout_reaches_the_persisted_key() -> None:
    """The named bug. PR #1570's WEB_LOADER_TIMEOUT=12 (a MEDIUM
    resource-exhaustion bound on the web-search page loader) reached the
    container environment and nowhere else, because web.loader.timeout was
    missing from RAG_CONFIG_ENV: `if WEB_LOADER_TIMEOUT:` in
    retrieval/web/utils.py read an empty persisted row forever."""
    config = FakeConfig({})
    applied = reconcile(config, {"WEB_LOADER_TIMEOUT": "12"})
    assert applied == {"web.loader.timeout": "12"}, applied


def test_searxng_query_url_reaches_the_persisted_key() -> None:
    """Found live on the deployed box while this fix was in flight: web
    search returned "An error occurred while searching the web" and the
    container log read "No SEARXNG_QUERY_URL found in environment
    variables", despite the container's own environment carrying it
    correctly. That message is misleading: routers/retrieval.py's searxng
    branch checks `config.SEARXNG_QUERY_URL` (the live, per-request Config
    store value from get_retrieval_config()), not os.environ."""
    config = FakeConfig({"web.search.searxng_query_url": ""})
    applied = reconcile(config, {"SEARXNG_QUERY_URL": "http://searxng:8080/search"})
    assert applied["web.search.searxng_query_url"] == "http://searxng:8080/search", applied


def test_rag_top_k_and_web_search_result_count_are_coerced_to_int() -> None:
    """Both flow into a `k=` argument a vector-store or search call passes
    straight through, and upstream stores them as ints (`int(os.getenv(...))`
    in config.py), so a persisted string would risk a TypeError deep in a
    request path rather than the value simply being ignored."""
    config = FakeConfig({})
    applied = reconcile(
        config, {"RAG_TOP_K": "8", "WEB_SEARCH_RESULT_COUNT": "5"}
    )
    assert applied["rag.top_k"] == 8 and isinstance(applied["rag.top_k"], int)
    assert applied["web.search.result_count"] == 5
    assert isinstance(applied["web.search.result_count"], int)


def test_a_non_numeric_int_key_is_refused_rather_than_crashing_downstream() -> None:
    config = FakeConfig({})
    try:
        reconcile(config, {"RAG_TOP_K": "five"})
    except RuntimeError as exc:
        assert "RAG_TOP_K" in str(exc), exc
    else:
        raise AssertionError("expected a refusal for a non-numeric RAG_TOP_K")
    assert config.upsert_calls == 0


def test_a_negative_int_key_is_refused_too() -> None:
    """PR #1582 review nit: int() accepts a negative value with no error, but
    a negative result count would still misbehave request-side rather than
    at boot, the exact gap this coercion exists to close."""
    config = FakeConfig({})
    try:
        reconcile(config, {"WEB_SEARCH_RESULT_COUNT": "-1"})
    except RuntimeError as exc:
        assert "WEB_SEARCH_RESULT_COUNT" in str(exc), exc
    else:
        raise AssertionError("expected a refusal for a negative WEB_SEARCH_RESULT_COUNT")
    assert config.upsert_calls == 0


def test_openai_connection_is_pointed_at_the_gateway() -> None:
    """The connection chat completions actually authenticate with
    (routers/openai.py.get_openai_connection reads openai.api_base_urls/
    openai.api_keys fresh from the Config store on every request). Persisted
    as single-element lists: upstream supports several named connections,
    Hive only ever wires the one at docker-compose.yml's
    OPENAI_API_BASE_URL/OPENAI_API_KEY."""
    config = FakeConfig({})
    applied = reconcile(
        config,
        {
            "OPENAI_API_BASE_URL": "http://edge-api:8080/v1",
            "OPENAI_API_KEY": "hk_example",
        },
    )
    assert applied["openai.api_base_urls"] == ["http://edge-api:8080/v1"], applied
    assert applied["openai.api_keys"] == ["hk_example"], applied


def test_openai_connection_url_without_a_key_is_refused() -> None:
    """The same posture as the RAG/audio destinations above: a rotated key
    must not leave a base URL pointed at a destination with no credential."""
    config = FakeConfig({})
    try:
        reconcile(config, {"OPENAI_API_BASE_URL": "http://edge-api:8080/v1"})
    except RuntimeError as exc:
        message = str(exc)
    else:
        raise AssertionError("expected a refusal for a base URL with no key")
    assert "OPENAI_API_BASE_URL" in message and "OPENAI_API_KEY" in message, message


def test_openai_api_keys_value_is_never_logged() -> None:
    """openai.api_keys carries OWUI_SHIM_KEY, a real registered Hive API key,
    the same secrecy posture as rag.openai.api_key above."""
    summary = hive_rag_env_config.log_summary(
        {"openai.api_keys": ["hk_example"], "openai.api_base_urls": ["http://edge-api:8080/v1"]}
    )
    assert "hk_example" not in summary, summary
    assert "openai.api_keys" in summary, summary


def test_openai_and_ollama_enable_and_the_remaining_1575_booleans() -> None:
    config = FakeConfig({})
    applied = reconcile(
        config,
        {
            "ENABLE_OPENAI_API": "true",
            "ENABLE_OLLAMA_API": "false",
            "ENABLE_COMMUNITY_SHARING": "false",
            "ENABLE_EVALUATION_ARENA_MODELS": "false",
        },
    )
    assert applied["openai.enable"] is True
    assert applied["ollama.enable"] is False
    assert applied["ui.enable_community_sharing"] is False
    assert applied["evaluation.arena.enable"] is False


def test_webui_url_and_default_locale_and_default_user_role_are_reconciled() -> None:
    """ui.default_user_role is the security-relevant one of the three:
    docker-compose.yml sets DEFAULT_USER_ROLE=pending deliberately so an
    unaffiliated login lands on the activation-pending screen rather than
    being granted app access (routers/auths.py reads
    Config.get('ui.default_user_role') on every new-account creation, both
    the password and the OAuth signup paths). A stale persisted "user" would
    silently reopen that door."""
    config = FakeConfig({})
    applied = reconcile(
        config,
        {"WEBUI_URL": "http://localhost:3003", "DEFAULT_LOCALE": "en", "DEFAULT_USER_ROLE": "pending"},
    )
    assert applied["webui.url"] == "http://localhost:3003"
    assert applied["ui.default_locale"] == "en"
    assert applied["ui.default_user_role"] == "pending"


def test_default_models_is_reconciled() -> None:
    """Issue #1626. `ui.default_models` is in upstream's DEFAULT_CONFIG, so a
    box that has booted once already has a row for it and no compose edit could
    reach that row. Unset, it publishes an empty default_models on /api/config
    and the chat front end opens every new conversation on whatever the model
    list happens to sort first, which is a Deepseek alias, not the alias this
    product leads with."""
    config = FakeConfig({"ui.default_models": ""})
    applied = reconcile(config, {"DEFAULT_MODELS": "hive-default"})
    assert applied["ui.default_models"] == "hive-default", applied
    assert config.stored["ui.default_models"] == "hive-default", config.stored


def test_unset_default_models_leaves_the_persisted_value_alone() -> None:
    """A deployment that lets its administrators pick the default through Open
    WebUI's own settings keeps their choice; this reconcile is per-key, not
    ENABLE_PERSISTENT_CONFIG=false."""
    for environ in ({}, {"DEFAULT_MODELS": "  "}):
        config = FakeConfig({"ui.default_models": "some-local-model"})
        applied = reconcile(config, environ)
        assert "ui.default_models" not in applied, applied
        assert config.stored["ui.default_models"] == "some-local-model", config.stored


def test_compose_names_a_default_model_and_keeps_ci_on_a_free_one() -> None:
    """The reconcile only helps if compose names a value. Asserted against the
    file because an unset variable is not an error anywhere: it silently
    restores the alphabetical-first fallback this fixes.

    The committed default has to be upstream-free. Every lane that starts a
    stack from this file opens its conversations on whatever it resolves to,
    so a paid alias here is a pipeline spending real budget on a schedule;
    TestNoCISurfaceCallsAPaidCompletionModel enforces that separately and this
    states the same requirement where the value is read."""
    compose = (
        Path(__file__).resolve().parents[1] / "deploy" / "docker" / "docker-compose.yml"
    ).read_text(encoding="utf-8")
    assert (
        "DEFAULT_MODELS: ${OWUI_DEFAULT_MODEL:-hive-free}" in compose
    ), "docker-compose.yml must name the alias new chats open on (issue #1626)"


def test_the_deploy_workflow_gives_the_demo_box_the_product_default() -> None:
    """The other half of that split. The compose default keeps pipelines off
    the paid catalogue; the deployed box is what #1626 is actually about, and
    it gets `hive-default` from the deploy workflow's environment, which wins
    over the box's own .env during interpolation."""
    workflow = (
        Path(__file__).resolve().parents[1]
        / ".github"
        / "workflows"
        / "deploy-demo-box.yml"
    ).read_text(encoding="utf-8")
    assert (
        'OWUI_DEFAULT_MODEL: "hive-default"' in workflow
    ), "deploy-demo-box.yml must give the demo box its product default (issue #1626)"


def test_ui_enable_signup_is_reconciled() -> None:
    """This deployment is SSO-only; the signup gate
    (routers/auths.py: `if not await Config.get('ui.enable_signup') or not
    await Config.get('ui.enable_login_form')`) reads this live, exactly like
    the login-form half of the same gate #722 already fixed."""
    config = FakeConfig({"ui.enable_signup": True})
    applied = reconcile(config, {"ENABLE_SIGNUP": "false"})
    assert applied["ui.enable_signup"] is False
    assert config.stored["ui.enable_signup"] is False


def _persisted_config_env_vars(source: str) -> dict:
    return hive_rag_env_config._persisted_config_env_vars(source)


def test_persisted_config_env_vars_detects_a_single_line_assignment() -> None:
    source = (
        "WEB_LOADER_TIMEOUT = os.getenv('WEB_LOADER_TIMEOUT', '')\n"
        "DEFAULT_CONFIG = {\n"
        "    'web.loader.timeout': WEB_LOADER_TIMEOUT,\n"
        "}\n"
    )
    mapping = _persisted_config_env_vars(source)
    assert mapping.get("WEB_LOADER_TIMEOUT") == ["web.loader.timeout"], mapping


def test_persisted_config_env_vars_detects_a_multiline_list_comprehension() -> None:
    """The exact shape that broke a naive per-line regex during the #1575
    audit: OAUTH_ALLOWED_ROLES/OAUTH_ADMIN_ROLES in the real pinned source
    are built with a multi-line list comprehension, not a plain assignment."""
    source = (
        "OAUTH_ADMIN_ROLES = [\n"
        "    role.strip()\n"
        "    for role in os.getenv('OAUTH_ADMIN_ROLES', 'admin').split(',')\n"
        "    if role\n"
        "]\n"
        "DEFAULT_CONFIG = {\n"
        "    'oauth.admin_roles': OAUTH_ADMIN_ROLES,\n"
        "}\n"
    )
    mapping = _persisted_config_env_vars(source)
    assert mapping.get("OAUTH_ADMIN_ROLES") == ["oauth.admin_roles"], mapping


def test_guard_would_have_caught_the_web_loader_timeout_bug() -> None:
    """Acceptance criterion 4's own proof: revert web.loader.timeout out of
    RAG_CONFIG_ENV (simulating this exact module before the #1575 fix) and
    confirm the guard, run against the real pinned vendor config.py source,
    actually raises rather than merely being expected to."""
    if not VENDOR_CONFIG_PATH.exists():
        return
    config_source = VENDOR_CONFIG_PATH.read_text(encoding="utf-8")
    original = hive_rag_env_config.RAG_CONFIG_ENV
    hive_rag_env_config.RAG_CONFIG_ENV = {
        key: value for key, value in original.items() if key != "web.loader.timeout"
    }
    try:
        try:
            hive_rag_env_config.guard_unreconciled_env_vars(
                {"WEB_LOADER_TIMEOUT": "12"}, config_source
            )
        except RuntimeError as exc:
            assert "WEB_LOADER_TIMEOUT" in str(exc), exc
        else:
            raise AssertionError("expected the guard to raise")
    finally:
        hive_rag_env_config.RAG_CONFIG_ENV = original


def test_guard_ignores_a_persisted_var_this_deployment_never_sets() -> None:
    if not VENDOR_CONFIG_PATH.exists():
        return
    config_source = VENDOR_CONFIG_PATH.read_text(encoding="utf-8")
    original = hive_rag_env_config.RAG_CONFIG_ENV
    hive_rag_env_config.RAG_CONFIG_ENV = {
        key: value for key, value in original.items() if key != "web.loader.timeout"
    }
    try:
        hive_rag_env_config.guard_unreconciled_env_vars({}, config_source)
    finally:
        hive_rag_env_config.RAG_CONFIG_ENV = original


def test_guard_does_not_flag_the_truly_environment_only_oauth_cluster() -> None:
    """Corrected post-review (PR #1582): only these five are read as frozen
    module constants (Authlib client registration, config.py:2600-2632) with
    no live Config.get/Config.get_many read anywhere in the pinned backend.
    The other eight members of the original 13-entry claim are covered by
    test_oauth_role_and_signup_cluster_is_reconciled_not_allowlisted below,
    since they are reconciled now, not allowlisted."""
    if not VENDOR_CONFIG_PATH.exists():
        return
    config_source = VENDOR_CONFIG_PATH.read_text(encoding="utf-8")
    hive_rag_env_config.guard_unreconciled_env_vars(
        {
            "OAUTH_CLIENT_ID": "test-client",
            "OAUTH_CLIENT_SECRET": "test-secret",
            "OAUTH_PROVIDER_NAME": "hive-sso",
            "OAUTH_SCOPES": "openid email profile",
            "OAUTH_CODE_CHALLENGE_METHOD": "S256",
        },
        config_source,
    )


def test_oauth_role_and_signup_cluster_is_reconciled_not_allowlisted() -> None:
    """The CRITICAL security-review finding on this PR. All eight of these
    are read live via utils/oauth.py's get_oauth_runtime_config()
    (Config.get_many against a dict-derived key list, the exact call shape a
    literal `Config.get('oauth.` grep cannot see), from handle_login,
    handle_callback and get_user_role, the last of which decides whether an
    SSO login is promoted to admin. docker-compose.yml sets seven of these
    eight today, so this was a live exposure, not only future drift."""
    config = FakeConfig({})
    applied = reconcile(
        config,
        {
            "ENABLE_OAUTH_SIGNUP": "true",
            "ENABLE_OAUTH_ROLE_MANAGEMENT": "true",
            "ENABLE_OAUTH_GROUP_MANAGEMENT": "true",
            "OAUTH_ROLES_CLAIM": "owui_role",
            "OAUTH_GROUPS_CLAIM": "tenants",
            "OAUTH_ALLOWED_ROLES": "ADMIN,MEMBER,VIEWER",
            "OAUTH_ADMIN_ROLES": "ADMIN",
            "OPENID_PROVIDER_URL": "https://sso.example/.well-known/openid-configuration",
        },
    )
    assert applied["oauth.enable_signup"] is True
    assert applied["oauth.enable_role_mapping"] is True
    assert applied["oauth.enable_group_mapping"] is True
    assert applied["oauth.roles_claim"] == "owui_role"
    assert applied["oauth.group_claim"] == "tenants"
    # Case-preserving: role identifiers are matched verbatim against an IdP
    # claim (OAuthManager.get_user_role), so "ADMIN" must not become "admin".
    assert applied["oauth.allowed_roles"] == ["ADMIN", "MEMBER", "VIEWER"], applied
    assert applied["oauth.admin_roles"] == ["ADMIN"], applied
    assert applied["oauth.provider_url"] == (
        "https://sso.example/.well-known/openid-configuration"
    )


def test_an_empty_oauth_role_list_is_refused_rather_than_disabling_role_matching() -> None:
    """The same "looks configured, does nothing" trap LIST_KEYS already
    refuses for file extensions, applied to the role/admin allow lists."""
    config = FakeConfig({})
    try:
        reconcile(config, {"OAUTH_ALLOWED_ROLES": " , ,"})
    except RuntimeError as exc:
        assert "OAUTH_ALLOWED_ROLES" in str(exc), exc
    else:
        raise AssertionError("expected a refusal for an all-separators role list")
    assert config.upsert_calls == 0


def test_the_five_correctly_environment_only_oauth_keys_are_untouched() -> None:
    """The negative half of the CRITICAL finding: these five must stay
    exactly as ENVIRONMENT_ONLY_ENV_VARS says, reconcile() writes nothing for
    them even when set, since Authlib's client registration reads the
    frozen os.environ constant, never the persisted row."""
    config = FakeConfig({"oauth.client_id": "persisted-client-id"})
    applied = reconcile(
        config,
        {
            "OAUTH_CLIENT_ID": "env-client-id",
            "OAUTH_CLIENT_SECRET": "env-secret",
            "OAUTH_PROVIDER_NAME": "hive-sso",
            "OAUTH_SCOPES": "openid email profile",
            "OAUTH_CODE_CHALLENGE_METHOD": "S256",
        },
    )
    assert applied == {}, applied
    assert config.stored["oauth.client_id"] == "persisted-client-id"


def test_guard_covers_every_env_var_docker_compose_actually_sets() -> None:
    """The strongest version of this guard's own self-check: every env var
    name docker-compose.yml's open-webui service block sets, fed to the
    guard against the real pinned vendor source, must raise nothing. This is
    what would have caught both #1575 instances (WEB_LOADER_TIMEOUT and
    SEARXNG_QUERY_URL) before either shipped, and it goes red automatically
    if a future compose edit adds a new persisted-config-backed variable
    without a matching reconcile entry."""
    if not VENDOR_CONFIG_PATH.exists():
        return
    compose = _compose_text()
    block = _compose_service_block(compose, "open-webui")
    variable_names = sorted(set(re.findall(r"^\s{6}([A-Z][A-Z0-9_]*):", block, re.MULTILINE)))
    assert "WEB_LOADER_TIMEOUT" in variable_names
    assert "SEARXNG_QUERY_URL" in variable_names
    # The CRITICAL finding's own vars: this run is what proves compose sets
    # them today, and that the guard raises nothing now that they are
    # reconciled instead of allowlisted.
    for oauth_var in (
        "ENABLE_OAUTH_SIGNUP",
        "ENABLE_OAUTH_ROLE_MANAGEMENT",
        "ENABLE_OAUTH_GROUP_MANAGEMENT",
        "OAUTH_ROLES_CLAIM",
        "OAUTH_ALLOWED_ROLES",
        "OAUTH_ADMIN_ROLES",
        "OAUTH_GROUPS_CLAIM",
    ):
        assert oauth_var in variable_names, (
            f"{oauth_var} is no longer set by docker-compose.yml's open-webui "
            f"service; the CRITICAL finding this test guards no longer "
            f"applies live, update the comment on ENVIRONMENT_ONLY_ENV_VARS"
        )
    # Every variable is set to a harmless placeholder; only its presence
    # (not its value) matters to the guard.
    environ = {name: "placeholder-value" for name in variable_names}
    config_source = VENDOR_CONFIG_PATH.read_text(encoding="utf-8")
    hive_rag_env_config.guard_unreconciled_env_vars(environ, config_source)


def test_tiktoken_and_whisper_model_are_reconciled_not_environment_only() -> None:
    """2026-08-30 outage: PR #1582 added the guard below with these two
    variables still missing from RAG_CONFIG_ENV. Both are baked into the
    pinned image's own Dockerfile (`ENV TIKTOKEN_ENCODING_NAME="cl100k_base"`,
    `ENV WHISPER_MODEL="base"`), not set by docker-compose.yml at all, so
    every container this image starts has them in os.environ regardless of
    compose -- which is exactly what the guard saw on the live box. See
    RAG_CONFIG_ENV's own comment for why each is read live (Config.get_many
    via RETRIEVAL_CONFIG_KEYS / STT_CONFIG_KEYS) rather than frozen."""
    if not VENDOR_CONFIG_PATH.exists():
        return
    assert "TIKTOKEN_ENCODING_NAME" in hive_rag_env_config.RAG_CONFIG_ENV.values()
    assert "WHISPER_MODEL" in hive_rag_env_config.RAG_CONFIG_ENV.values()
    config_source = VENDOR_CONFIG_PATH.read_text(encoding="utf-8")
    # Must not raise: both are reconciled now, so setting them is unremarkable.
    hive_rag_env_config.guard_unreconciled_env_vars(
        {"TIKTOKEN_ENCODING_NAME": "cl100k_base", "WHISPER_MODEL": "base"},
        config_source,
    )


def test_guard_would_have_caught_the_2026_08_30_outage() -> None:
    """Reproduces the exact pre-fix state (issue #1575's original PR #1582
    shipped without these two) and proves the guard, run against the real
    pinned vendor source, raises on both rather than merely being expected
    to. This is the regression guard for the incident itself, distinct from
    the pre-existing WEB_LOADER_TIMEOUT one above."""
    if not VENDOR_CONFIG_PATH.exists():
        return
    config_source = VENDOR_CONFIG_PATH.read_text(encoding="utf-8")
    original = hive_rag_env_config.RAG_CONFIG_ENV
    hive_rag_env_config.RAG_CONFIG_ENV = {
        key: value
        for key, value in original.items()
        if key not in ("rag.tiktoken_encoding_name", "audio.stt.whisper_model")
    }
    try:
        try:
            hive_rag_env_config.guard_unreconciled_env_vars(
                {"TIKTOKEN_ENCODING_NAME": "cl100k_base", "WHISPER_MODEL": "base"},
                config_source,
            )
        except RuntimeError as exc:
            assert "TIKTOKEN_ENCODING_NAME" in str(exc), exc
            assert "WHISPER_MODEL" in str(exc), exc
        else:
            raise AssertionError("expected the guard to raise")
    finally:
        hive_rag_env_config.RAG_CONFIG_ENV = original


def test_guard_ci_default_still_raises_when_detection_is_removed() -> None:
    """The CI-facing half of the fatal/non-fatal split: with no `fatal`
    argument (the default), a gap is still a raise, i.e. still a red build.
    Deleting the guard's own detection call (comment out the `if not gaps`
    early return, or stop calling this test) is what would turn this green
    for the wrong reason; the assertion below is on the exception itself, not
    merely on the test completing, so a no-op detection fails this loudly."""
    if not VENDOR_CONFIG_PATH.exists():
        return
    config_source = VENDOR_CONFIG_PATH.read_text(encoding="utf-8")
    original = hive_rag_env_config.RAG_CONFIG_ENV
    hive_rag_env_config.RAG_CONFIG_ENV = {
        key: value for key, value in original.items() if key != "web.loader.timeout"
    }
    try:
        raised = False
        try:
            hive_rag_env_config.guard_unreconciled_env_vars(
                {"WEB_LOADER_TIMEOUT": "12"}, config_source
            )
        except RuntimeError:
            raised = True
        assert raised, "CI must fail hard on an unreconciled variable"
    finally:
        hive_rag_env_config.RAG_CONFIG_ENV = original


def test_guard_boot_mode_reports_without_raising() -> None:
    """The boot-facing half of the fatal/non-fatal split (2026-08-30
    postmortem): the same gap that must fail CI must NOT raise at boot, only
    log at ERROR and let the caller continue. Verified two ways: the call
    itself must not raise, and a real logging record naming the variable must
    have been emitted -- a silently-swallowed finding would be as dangerous
    as the outage this exists to prevent."""
    if not VENDOR_CONFIG_PATH.exists():
        return
    config_source = VENDOR_CONFIG_PATH.read_text(encoding="utf-8")
    original = hive_rag_env_config.RAG_CONFIG_ENV
    hive_rag_env_config.RAG_CONFIG_ENV = {
        key: value for key, value in original.items() if key != "web.loader.timeout"
    }

    class _CaptureHandler(logging.Handler):
        def __init__(self) -> None:
            super().__init__()
            self.records: list[str] = []

        def emit(self, record: logging.LogRecord) -> None:
            self.records.append(record.getMessage())

    capture = _CaptureHandler()
    hive_rag_env_config._logger.addHandler(capture)
    hive_rag_env_config._logger.setLevel(logging.ERROR)
    try:
        hive_rag_env_config.guard_unreconciled_env_vars(
            {"WEB_LOADER_TIMEOUT": "12"}, config_source, fatal=False
        )
    finally:
        hive_rag_env_config._logger.removeHandler(capture)
        hive_rag_env_config.RAG_CONFIG_ENV = original

    assert any("WEB_LOADER_TIMEOUT" in message for message in capture.records), (
        f"boot mode must log the finding at ERROR, got: {capture.records}"
    )


def _boot_splice_ast() -> ast.Module:
    """Parse the fragment apply_rag_env_config_patch.py splices into Open
    WebUI's own config.py, rather than substring-matching it, so reformatting
    the call does not produce a false green and deleting a keyword does
    produce a red."""
    patch_source = PATCH_PATH.read_text(encoding="utf-8")
    insert = next(
        node.value.value
        for node in ast.parse(patch_source).body
        if isinstance(node, ast.Assign)
        and isinstance(node.targets[0], ast.Name)
        and node.targets[0].id == "INSERT"
    )
    return ast.parse(textwrap.dedent(insert))


def _boot_splice_call(tree: ast.Module, name: str) -> ast.Call:
    return next(
        node
        for node in ast.walk(tree)
        if isinstance(node, ast.Call) and getattr(node.func, "id", None) == name
    )


def test_boot_call_site_passes_fatal_false() -> None:
    """The 2026-08-30 outage was one keyword argument's absence. The
    function-level tests above prove a non-fatal mode exists; only this one
    proves the boot path uses it, for both calls on the splice (PR #1588
    review). Verified red before green by deleting fatal=False from each call
    in turn."""
    tree = _boot_splice_ast()
    for name in ("guard_unreconciled_env_vars", "reconcile"):
        call = _boot_splice_call(tree, name)
        assert any(
            keyword.arg == "fatal" and keyword.value.value is False
            for keyword in call.keywords
        ), (
            f"the boot call site must pass fatal=False to {name}; see the "
            f"2026-08-30 postmortem"
        )


def test_boot_call_site_wraps_the_config_source_audit() -> None:
    """inspect.getsource is evaluated as an ARGUMENT to the guard, so its
    OSError (source file unavailable) and TypeError (module not locatable)
    escape before the function is entered and fatal=False never sees them.
    The call has to sit inside a try/except for the guard's promise to hold
    (PR #1588 review)."""
    tree = _boot_splice_ast()
    guarded = False
    for node in ast.walk(tree):
        if not isinstance(node, ast.Try):
            continue
        for statement in node.body:
            for inner in ast.walk(statement):
                if (
                    isinstance(inner, ast.Call)
                    and getattr(inner.func, "id", None)
                    == "guard_unreconciled_env_vars"
                ):
                    guarded = True
    assert guarded, (
        "the guard call must sit inside a try/except: getsource() raises "
        "before fatal=False can be consulted"
    )


def test_every_refusal_in_the_reconcile_path_honours_fatal() -> None:
    """The guard was made non-fatal at boot while overrides, called one line
    later on the same splice, still had eight raises that aborted startup (PR
    #1588 review). They route through _refuse now. This goes red if a new
    refusal is added as a bare raise in any of the three functions the boot
    path reaches, which is how the eight got there in the first place."""
    tree = ast.parse(MODULE_PATH.read_text(encoding="utf-8"))
    reached = ("derived_upload_cap", "openai_connection_override", "overrides")
    for node in tree.body:
        if not isinstance(node, ast.FunctionDef) or node.name not in reached:
            continue
        raises = [inner for inner in ast.walk(node) if isinstance(inner, ast.Raise)]
        assert not raises, (
            f"{node.name} still raises; a refusal on the boot path must go "
            f"through _refuse(message, fatal) so fatal=False degrades to a "
            f"log instead of a crash loop"
        )
        assert any(
            isinstance(inner, ast.Call)
            and getattr(inner.func, "id", None) == "_refuse"
            for inner in ast.walk(node)
        ), f"{node.name} no longer refuses anything, which is suspicious"


def test_boot_mode_skips_a_bad_value_and_reconciles_the_rest() -> None:
    """A malformed value must cost its own key and nothing else. Before this,
    one bad RAG_TOP_K aborted the whole boot, which also meant the
    SEARXNG_QUERY_URL reconcile that fixes web search never ran."""
    environ = dict(
        RAG_TOP_K="not a number",
        RAG_EMBEDDING_MODEL=ALIAS,
        SEARXNG_QUERY_URL=SEARX_URL,
    )
    capture = _ErrorCapture()
    hive_rag_env_config._logger.addHandler(capture)
    hive_rag_env_config._logger.setLevel(logging.ERROR)
    try:
        applied = hive_rag_env_config.overrides(environ, fatal=False)
    finally:
        hive_rag_env_config._logger.removeHandler(capture)
    assert "rag.top_k" not in applied, (
        f"a refused value must leave its own persisted row alone, got {applied}"
    )
    assert applied["rag.embedding_model"] == ALIAS
    assert applied["web.search.searxng_query_url"] == SEARX_URL
    assert any("RAG_TOP_K" in message for message in capture.messages), (
        f"boot mode must log the refusal at ERROR, got: {capture.messages}"
    )


def test_boot_mode_never_persists_a_half_paired_destination() -> None:
    """The one refusal where skipping has to do more than continue: a base URL
    without its credential must be dropped from the write rather than
    persisted alone, or the embedder would be repointed while Open WebUI kept
    sending the key issued for the previous destination."""
    capture = _ErrorCapture()
    hive_rag_env_config._logger.addHandler(capture)
    hive_rag_env_config._logger.setLevel(logging.ERROR)
    try:
        applied = hive_rag_env_config.overrides(
            dict(RAG_OPENAI_API_BASE_URL=GATEWAY_URL), fatal=False
        )
    finally:
        hive_rag_env_config._logger.removeHandler(capture)
    assert any("RAG_OPENAI_API_KEY" in m for m in capture.messages), (
        f"boot mode must log the refusal at ERROR, got: {capture.messages}"
    )
    assert "rag.openai_api_base_url" not in applied, (
        f"a destination without its credential must not be persisted, got "
        f"{applied}"
    )


def test_ci_default_still_refuses_every_boot_path_value() -> None:
    """The other half of the split: making boot non-fatal must not have made
    CI permissive. One case per refusal, at the fatal=True default."""
    cases = (
        dict(RAG_FILE_MAX_SIZE="100"),
        dict(RAG_MAX_UPLOAD_BYTES="twenty five"),
        dict(RAG_MAX_UPLOAD_BYTES="1024"),
        dict(OPENAI_API_BASE_URL=GATEWAY_URL),
        dict(RAG_ALLOWED_FILE_EXTENSIONS=" . , "),
        dict(OAUTH_ALLOWED_ROLES=" , "),
        dict(RAG_TOP_K="-5"),
        dict(RAG_OPENAI_API_BASE_URL=GATEWAY_URL),
    )
    for environ in cases:
        raised = False
        try:
            hive_rag_env_config.overrides(dict(environ))
        except RuntimeError:
            raised = True
        assert raised, f"CI must still refuse {environ}"


def test_web_fetch_content_cap_is_reconciled() -> None:
    """Issue #1639. `web.fetch.max_content_length` is in upstream's
    DEFAULT_CONFIG, so it was seeded on the very first boot and thereafter
    read from Open WebUI's own database: absent from this map, no compose
    change could ever reach the demo box's already-booted volume, and the cap
    read as configured while being whatever the first boot happened to write.
    Same class as #1575, and it stays worth reconciling until slice S7 retires
    the fork's own fetch path entirely."""
    applied = hive_rag_env_config.overrides(dict(WEB_FETCH_MAX_CONTENT_LENGTH="50000"))
    assert applied["web.fetch.max_content_length"] == 50000, applied
    assert isinstance(applied["web.fetch.max_content_length"], int), applied


def test_web_fetch_content_cap_refuses_zero_and_malformed_values() -> None:
    """Upstream gates the truncation on `max_length > 0` (tools/builtin.py),
    so a persisted 0 turns the cap off entirely while the configuration still
    names one. A cap that is inert while looking set is the defect this row
    exists to end, so zero is refused rather than written."""
    for value in ("0", "-1", "1.5", "lots"):
        raised = False
        try:
            hive_rag_env_config.overrides(dict(WEB_FETCH_MAX_CONTENT_LENGTH=value))
        except RuntimeError:
            raised = True
        assert raised, f"must refuse WEB_FETCH_MAX_CONTENT_LENGTH={value!r}"


def test_web_fetch_content_cap_unset_leaves_the_persisted_value_alone() -> None:
    applied = hive_rag_env_config.overrides(dict(WEB_FETCH_MAX_CONTENT_LENGTH="  "))
    assert "web.fetch.max_content_length" not in applied


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui RAG env-config reconcile (issue #722)")


if __name__ == "__main__":
    sys.exit(main())
