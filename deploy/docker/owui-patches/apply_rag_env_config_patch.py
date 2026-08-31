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
    # fatal=False: see this file's module docstring for why boot must
    # never abort over this, only CI (scripts/test_owui_rag_env_config.py
    # calls the same function at its fatal=True default).
    guard_unreconciled_env_vars(
        os.environ, _hive_inspect.getsource(_hive_owui_config), fatal=False
    )

    _hive_rag_applied = await reconcile(Config, os.environ)
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
