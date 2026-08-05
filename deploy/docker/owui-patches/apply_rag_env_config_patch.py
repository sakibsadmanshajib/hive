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
    from open_webui.utils.hive_rag_env_config import log_summary, reconcile

    _hive_rag_applied = await reconcile(Config, os.environ)
    if _hive_rag_applied:
        log.info(
            'hive: reconciled Open WebUI RAG config from env: %s',
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
