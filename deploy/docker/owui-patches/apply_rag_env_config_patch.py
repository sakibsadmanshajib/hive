"""Build-time splice: call hive_rag_env_config.reconcile at the end of Open
WebUI's seed_registered_defaults, so the RAG-through-Hive keys follow the
container environment on every start instead of being frozen at whatever the
first boot persisted (issue #722).

seed_registered_defaults is the right anchor because Open WebUI's own lifespan
runs it immediately before initialize_runtime_config, which is where the
embedding function is constructed from those very keys. Reconciling anywhere
later would leave the already-built embedder pointing at the stale model.

Asserts its own effect and fails the build otherwise, the same posture as this
Dockerfile's other patches: a future open-webui digest bump whose startup path
shifted breaks the build loudly rather than silently reverting to the broken
behaviour. Checking that the anchor string exists is not enough on its own, so
this also asserts that the names the inserted code closes over (Config, os,
log) are still in that module's namespace.

Issue #1575 added a second call ahead of reconcile: guard_unreconciled_env_vars
reads this exact process's own config.py source and reports before anything is
reconciled if the deployment sets a variable that backs a persisted config key
neither reconcile() nor ENVIRONMENT_ONLY_ENV_VARS accounts for. See
hive_rag_env_config.py for the full audit that motivated it.

Called here with fatal=False (2026-08-30 postmortem, PR #1587 is the incident
revert). The guard correctly found two unreconciled variables on the live demo
box, and the guard's own default of raising turned that correct finding into
FastAPI startup aborting, which crash-looped the container and took chat down
with a 502. A boot-time abort converts a config-hygiene gap into an outage,
and an outage is a worse failure than the drift it prevents. fatal=False keeps
the finding loud (ERROR-level log naming every gap, still on every boot) but
lets the service start; scripts/test_owui_rag_env_config.py keeps the default
fatal=True path so the same gap still fails CI, which is the right blast
radius for this class of bug.

reconcile is called with fatal=False for the same reason, and the guard call
is additionally wrapped: three things could abort this boot, and all three are
now covered.

1. The guard's own finding. fatal=False, above.
2. The argument evaluated before the guard is entered.
   inspect.getsource raises OSError when the module's source file is
   unavailable and TypeError for a module it cannot locate, neither of which
   fatal=False can intercept because the function has not been called yet, so
   the call itself is wrapped and a failed audit is logged and skipped.
3. reconcile's own value validation. Eight checks inside
   hive_rag_env_config.overrides refuse a malformed or unpaired value, and
   before this change every one of them aborted FastAPI startup one line after
   the guard was made safe. At fatal=False each instead logs at ERROR and
   leaves that single persisted row untouched, which is this module's existing
   contract for a variable that is not set, so a bad value degrades to "that
   one key keeps its old value, loudly" rather than to a crash loop. CI still
   calls overrides at its fatal=True default, so every one of those checks
   still fails a build.
"""

import ast
import pathlib
import re

TARGET = pathlib.Path("/app/backend/open_webui/config.py")
SIGNATURE = "async def seed_registered_defaults():\n"
ANCHOR = "    await Config.seed_defaults(DEFAULT_CONFIG)\n"
INSERT = """    # hive #722: seed_defaults above only fills keys that are ABSENT, so a
    # container whose database was seeded by an older compose keeps sending
    # that generation's embedding model forever and no compose change can
    # reach it. Let the environment win for the RAG-through-Hive keys only.
    import inspect as _hive_inspect

    from open_webui import config as _hive_owui_config
    from open_webui.utils.hive_rag_env_config import (
        guard_unreconciled_env_vars,
        log_summary,
        reconcile,
    )

    # hive #1575 / 2026-08-30 postmortem: report loudly, before reconciling
    # anything, if this deployment sets an environment variable that backs a
    # persisted config key neither reconciled below nor acknowledged as
    # environment-only. fatal=False: a boot must never abort over this (see
    # this file's module docstring), only CI does. Reads this exact process's
    # own already-imported config module, so a future upstream digest bump is
    # covered automatically rather than needing a hand-kept list to stay
    # current.
    #
    # Wrapped in try/except because getsource() is evaluated as an ARGUMENT,
    # so it runs BEFORE the guard is entered: it raises OSError when the
    # source file is unavailable and TypeError for a module it cannot locate,
    # and either one would abort startup without fatal=False ever being
    # consulted. The whole point of this call is that config hygiene cannot
    # take a live service down, so the audit is allowed to be skipped and
    # never allowed to be fatal.
    try:
        guard_unreconciled_env_vars(
            os.environ, _hive_inspect.getsource(_hive_owui_config), fatal=False
        )
    except Exception:
        log.exception(
            'hive #1575 guard: skipped, could not audit this container config'
        )

    # fatal=False here too: reconcile's own value validation (the upload
    # ceiling, the OpenAI connection pairing, the list and integer coercions)
    # raises by default, and at boot a refused value must degrade to leaving
    # that one persisted row alone, not to a crash loop. CI calls the same
    # code at the fatal=True default.
    _hive_rag_applied = await reconcile(Config, os.environ, fatal=False)
    if _hive_rag_applied:
        log.info(
            'hive: reconciled Open WebUI config from env: %s',
            log_summary(_hive_rag_applied),
        )
"""

text = TARGET.read_text()

assert text.count(SIGNATURE) == 1, (
    "seed_registered_defaults is not defined exactly once -- upstream "
    "open-webui source shifted, patch needs updating"
)
assert text.count(ANCHOR) == 1, (
    "the 'await Config.seed_defaults(DEFAULT_CONFIG)' anchor is not present "
    "exactly once -- upstream open-webui source shifted, patch needs updating"
)

# The function body is everything from its signature to the next top-level
# statement, so we can prove the anchor belongs to this function and not to
# some other caller of seed_defaults.
sig_start = text.index(SIGNATURE)
body_start = sig_start + len(SIGNATURE)
next_top_level = re.search(r"\n\S", text[body_start:])
body_end = body_start + next_top_level.start() if next_top_level else len(text)
assert ANCHOR in text[body_start:body_end], (
    "the seed_defaults anchor is not inside seed_registered_defaults' own "
    "body -- upstream open-webui source shifted, patch needs updating"
)

# Names the inserted block closes over.
assert "from open_webui.models.config import Config\n" in text, (
    "config.py no longer imports Config -- patch needs updating"
)
assert re.search(r"^import os$", text, re.MULTILINE), (
    "config.py no longer imports os -- patch needs updating"
)
assert re.search(r"^\s+log,$", text, re.MULTILINE), (
    "config.py no longer imports log from open_webui.env -- patch needs updating"
)

patched = text.replace(ANCHOR, ANCHOR + INSERT, 1)
ast.parse(patched)  # never write a config.py that cannot be imported
TARGET.write_text(patched)
