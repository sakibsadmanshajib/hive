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


def test_reconciled_keys_are_loggable_without_the_secret() -> None:
    """The startup log line names the model (the signal this investigation
    lacked twice) and never the API key value."""
    summary = hive_rag_env_config.log_summary(
        {"rag.embedding_model": ALIAS, "rag.openai.api_key": "hk_secret"}
    )
    assert ALIAS in summary, summary
    assert "hk_secret" not in summary, summary
    assert "rag.openai.api_key" in summary, summary


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui RAG env-config reconcile (issue #722)")


if __name__ == "__main__":
    sys.exit(main())
