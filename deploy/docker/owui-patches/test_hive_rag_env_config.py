"""Regression tests for hive_rag_env_config.py (issue #1575).

No pytest: this repo has no Python test infra for deploy/docker/owui-patches/
*.py (see deploy/docker/pipelines/test_hive_agent_console_action.py for the
established precedent), and the module under test is pure stdlib (copy, re),
so plain unittest run directly is enough:

    python3 deploy/docker/owui-patches/test_hive_rag_env_config.py

Covers three things. First, the named bug: WEB_LOADER_TIMEOUT reaches
web.loader.timeout. Second, the two other genuine gaps that needed new
machinery rather than a plain dict entry (INT_KEYS coercion, the paired
openai.api_base_urls/openai.api_keys connection). Third, and this is the
real deliverable per the issue, guard_unreconciled_env_vars: proof that an
environment variable backing a persisted config key with no reconcile entry
is actually detected, against the real pinned vendor source, not a synthetic
stand-in.
"""

from __future__ import annotations

import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import hive_rag_env_config as h  # noqa: E402

REPO_ROOT = pathlib.Path(__file__).resolve().parents[3]
VENDOR_CONFIG_PATH = (
    REPO_ROOT / "vendor" / "open-webui" / "backend" / "open_webui" / "config.py"
)


class OverridesWebLoaderTimeoutTest(unittest.TestCase):
    """Issue #1575 acceptance criterion 1: the named bug."""

    def test_web_loader_timeout_reaches_the_persisted_key(self):
        applied = h.overrides({"WEB_LOADER_TIMEOUT": "12"})
        self.assertEqual(applied.get("web.loader.timeout"), "12")

    def test_unset_web_loader_timeout_writes_nothing(self):
        applied = h.overrides({})
        self.assertNotIn("web.loader.timeout", applied)


class IntKeysTest(unittest.TestCase):
    def test_rag_top_k_coerced_to_int(self):
        applied = h.overrides({"RAG_TOP_K": "8"})
        self.assertEqual(applied.get("rag.top_k"), 8)
        self.assertIsInstance(applied["rag.top_k"], int)

    def test_web_search_result_count_coerced_to_int(self):
        applied = h.overrides({"WEB_SEARCH_RESULT_COUNT": "5"})
        self.assertEqual(applied.get("web.search.result_count"), 5)

    def test_non_numeric_int_key_raises(self):
        with self.assertRaises(RuntimeError):
            h.overrides({"RAG_TOP_K": "five"})

    def test_negative_int_key_is_accepted(self):
        # Not upstream-meaningful, but int() itself accepts it and this
        # module's job is "does not silently mis-coerce", not "validates
        # every domain rule upstream might have".
        applied = h.overrides({"RAG_TOP_K": "-1"})
        self.assertEqual(applied.get("rag.top_k"), -1)


class OpenAIConnectionOverrideTest(unittest.TestCase):
    def test_neither_set_yields_nothing(self):
        self.assertEqual(h.openai_connection_override({}), {})

    def test_url_without_key_raises(self):
        with self.assertRaises(RuntimeError):
            h.openai_connection_override({"OPENAI_API_BASE_URL": "http://edge-api:8080/v1"})

    def test_paired_values_persist_as_single_element_lists(self):
        applied = h.openai_connection_override(
            {
                "OPENAI_API_BASE_URL": "http://edge-api:8080/v1",
                "OPENAI_API_KEY": "hk_test_shim",
            }
        )
        self.assertEqual(applied["openai.api_base_urls"], ["http://edge-api:8080/v1"])
        self.assertEqual(applied["openai.api_keys"], ["hk_test_shim"])

    def test_openai_api_keys_value_never_logged(self):
        applied = {"openai.api_keys": ["hk_test_shim"], "openai.api_base_urls": ["u"]}
        summary = h.log_summary(applied)
        self.assertNotIn("hk_test_shim", summary)
        self.assertIn("openai.api_keys", summary)


class FeatureConfigBooleanTest(unittest.TestCase):
    def test_ui_enable_signup_reconciled(self):
        applied = h.overrides({"ENABLE_SIGNUP": "false"})
        self.assertIs(applied.get("ui.enable_signup"), False)

    def test_openai_and_ollama_enable_reconciled(self):
        applied = h.overrides({"ENABLE_OPENAI_API": "true", "ENABLE_OLLAMA_API": "false"})
        self.assertIs(applied.get("openai.enable"), True)
        self.assertIs(applied.get("ollama.enable"), False)


class PersistedConfigEnvVarsTest(unittest.TestCase):
    """_persisted_config_env_vars: the guard's own source-scanning heuristic."""

    def test_single_line_assignment_detected(self):
        source = (
            "WEB_LOADER_TIMEOUT = os.getenv('WEB_LOADER_TIMEOUT', '')\n"
            "DEFAULT_CONFIG = {\n"
            "    'web.loader.timeout': WEB_LOADER_TIMEOUT,\n"
            "}\n"
        )
        mapping = h._persisted_config_env_vars(source)
        self.assertEqual(mapping.get("WEB_LOADER_TIMEOUT"), ["web.loader.timeout"])

    def test_multiline_list_comprehension_assignment_detected(self):
        # The exact shape that broke a naive per-line regex during the
        # #1575 audit: OAUTH_ALLOWED_ROLES/OAUTH_ADMIN_ROLES in the real
        # pinned source.
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
        mapping = h._persisted_config_env_vars(source)
        self.assertEqual(mapping.get("OAUTH_ADMIN_ROLES"), ["oauth.admin_roles"])

    def test_variable_with_no_default_config_entry_is_absent(self):
        source = "SOME_INTERNAL = os.getenv('SOME_INTERNAL', '')\nDEFAULT_CONFIG = {}\n"
        mapping = h._persisted_config_env_vars(source)
        self.assertEqual(mapping, {})


class GuardUnreconciledEnvVarsTest(unittest.TestCase):
    """Issue #1575 acceptance criterion 4: the real deliverable.

    This is the test the issue asks to revert-and-confirm-red: it monkeypatches
    RAG_CONFIG_ENV to remove the web.loader.timeout entry (simulating the
    pre-fix state of this exact module) and proves the guard, run against the
    real pinned vendor/open-webui config.py source, actually raises.
    """

    def setUp(self):
        if not VENDOR_CONFIG_PATH.exists():
            self.skipTest(f"vendor source not present at {VENDOR_CONFIG_PATH}")
        self.config_source = VENDOR_CONFIG_PATH.read_text()
        self.original_rag_config_env = h.RAG_CONFIG_ENV

    def tearDown(self):
        h.RAG_CONFIG_ENV = self.original_rag_config_env

    def test_guard_passes_against_real_vendor_source_today(self):
        # Sanity check: the CURRENT, fixed state of this module raises
        # nothing against every env var this deployment actually sets.
        environ = {
            "WEB_LOADER_TIMEOUT": "12",
            "SEARXNG_QUERY_URL": "http://searxng:8080/search",
            "WEBUI_URL": "http://localhost:3003",
            "DEFAULT_LOCALE": "en",
            "DEFAULT_USER_ROLE": "pending",
            "RAG_TOP_K": "5",
            "WEB_SEARCH_RESULT_COUNT": "5",
            "ENABLE_SIGNUP": "false",
            "ENABLE_OPENAI_API": "true",
            "ENABLE_OLLAMA_API": "false",
            "ENABLE_COMMUNITY_SHARING": "false",
            "ENABLE_EVALUATION_ARENA_MODELS": "false",
            "OPENAI_API_BASE_URL": "http://edge-api:8080/v1",
            "OPENAI_API_KEY": "hk_test_shim",
            # The OAuth cluster: set, persisted-config-backed, and
            # deliberately environment-only. Must not raise.
            "OAUTH_CLIENT_ID": "test-client",
            "OAUTH_CLIENT_SECRET": "test-secret",
            "OAUTH_ALLOWED_ROLES": "user,admin",
        }
        h.guard_unreconciled_env_vars(environ, self.config_source)

    def test_guard_would_have_caught_the_original_bug_when_reverted(self):
        # Simulate the pre-#1575 state of this exact file: web.loader.timeout
        # absent from RAG_CONFIG_ENV, everything else unchanged.
        h.RAG_CONFIG_ENV = {
            key: value
            for key, value in self.original_rag_config_env.items()
            if key != "web.loader.timeout"
        }
        with self.assertRaises(RuntimeError) as ctx:
            h.guard_unreconciled_env_vars({"WEB_LOADER_TIMEOUT": "12"}, self.config_source)
        self.assertIn("WEB_LOADER_TIMEOUT", str(ctx.exception))

    def test_guard_ignores_a_persisted_var_the_deployment_never_sets(self):
        h.RAG_CONFIG_ENV = {
            key: value
            for key, value in self.original_rag_config_env.items()
            if key != "web.loader.timeout"
        }
        # WEB_LOADER_TIMEOUT backs a persisted key and is unreconciled here,
        # but an unset variable is not this deployment's problem to raise on.
        h.guard_unreconciled_env_vars({}, self.config_source)

    def test_guard_does_not_flag_the_environment_only_oauth_cluster(self):
        h.guard_unreconciled_env_vars(
            {
                "OAUTH_CLIENT_ID": "test-client",
                "OAUTH_CLIENT_SECRET": "test-secret",
                "OAUTH_PROVIDER_NAME": "hive-sso",
                "OPENID_PROVIDER_URL": "https://sso.example/.well-known/openid-configuration",
                "OAUTH_SCOPES": "openid email profile",
                "OAUTH_CODE_CHALLENGE_METHOD": "S256",
                "OAUTH_GROUPS_CLAIM": "tenants",
                "ENABLE_OAUTH_SIGNUP": "true",
                "ENABLE_OAUTH_GROUP_MANAGEMENT": "true",
                "ENABLE_OAUTH_ROLE_MANAGEMENT": "true",
                "OAUTH_ROLES_CLAIM": "roles",
                "OAUTH_ALLOWED_ROLES": "user,admin",
                "OAUTH_ADMIN_ROLES": "admin",
            },
            self.config_source,
        )


if __name__ == "__main__":
    unittest.main()
