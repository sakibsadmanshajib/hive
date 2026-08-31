"""Build-time splice: give the chat surface a system prompt at all (issue #1596).

Before this patch, Hive's chat surface sent NO system prompt. Open WebUI's only
system-prompt inputs are `params.system` on a row of its own `models` table and
the per-user Settings > General field, and Hive's model list is synthesized by
`owui-patches/hive_model_picker.py` from the control-plane catalog instead of
being read from that table. The catalog has no system-prompt column and there
is no durable Open WebUI row to carry one, so neither input was reachable for a
Hive model. The consequence, which the #1596 audit established rather than
guessed: no identity, no capability statement, no citation rule and no refusal
guidance shipped, and every customer was talking to whatever default the routed
upstream provider happens to apply.

This inserts the deployment's own prompt, read from the `hive.chat.system_prompt`
persisted config row, at the front of the request's system message. That row is
reconciled from `HIVE_CHAT_SYSTEM_PROMPT` on every boot by
`owui-patches/hive_rag_env_config.py`, so the value is set in the deploy
workflow's env (versioned, reviewable) rather than in a database nobody can
grep. An unset variable leaves the row absent and this block does nothing at
all, so a deployment that configures nothing keeps today's behaviour byte for
byte.

Applied here rather than in vendor/open-webui because the chat image builds only
the FRONTEND from the vendored tree and takes the backend from the pinned
upstream image (see Dockerfile.open-webui), so a backend edit under vendor/ is
inert.

Position inside `process_chat_payload` is load bearing twice over, and both
halves are asserted below rather than left to a comment.

BEFORE `metadata['system_prompt']` is captured. That snapshot is what the
native tool-call loop restores verbatim (`original_system_content =
metadata.get('system_prompt')`, then `replace_system_message_content`) once a
tool call produces citations. A splice after the snapshot would put the prompt
on the first request of a turn and silently drop it from every request after the
first tool call, which is the shape of defect that reads as working.

AFTER the Chat Controls / User Settings branch, using
`add_or_update_system_message`'s default `append=False`, which PREPENDS
(`utils/misc.update_message_content`). So the order is Hive's block first, then
whatever the user or a model row supplied. A user filling in Settings > General
adds to this block and cannot delete it.

Asserts its own effect and fails the build otherwise, the same posture as this
Dockerfile's other patches. Behaviour is covered by
scripts/test_owui_chat_system_prompt.py, and end to end against a real booted
container by scripts/test_owui_prompt_template_delivery.py.
"""

import ast
import os
import pathlib
import re

TARGET = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_MIDDLEWARE_PY",
        "/app/backend/open_webui/utils/middleware.py",
    )
)

MARKER = "# hive (#1596)"

# The Hive-owned persisted config key. Kept identical to the one
# owui-patches/hive_rag_env_config.py reconciles; a test asserts the two agree,
# because a mismatch would write a row nothing reads and leave the deployment
# looking configured.
CONFIG_KEY = "hive.chat.system_prompt"

SIGNATURE = "async def process_chat_payload(request, form_data, user, metadata, model):\n"

# The pre-RAG snapshot. Inserting immediately above it is what puts the prompt
# inside metadata['system_prompt'], which is what makes it survive the
# tool-call restore.
ANCHOR = (
    "    # Save the pre-RAG message state so the native tool call loop can\n"
)

SNAPSHOT = "    metadata['system_prompt'] = system_content or None\n"

RESTORE = "original_system_content = metadata.get('system_prompt')"

INSERT = f"""    {MARKER}: the deployment's own system prompt, in front of everything else
    # in the system message. Open WebUI ships no global system prompt, and
    # neither of its two inputs (a models-table row's params.system, the
    # per-user Settings > General field) is reachable for a Hive model, whose
    # listing is synthesized from the control-plane catalog. See
    # apply_chat_system_prompt_patch.py for why this exact position, above the
    # snapshot below and below the Chat Controls branch above, is the only one
    # that both survives the native tool-call restore and stays in front of
    # the user's own system text.
    #
    # The isinstance test is not defensive noise. This runs on the chat hot
    # path and the row is writable by anything holding the database, so a
    # value of the wrong type has two bad outcomes to choose between: calling
    # .strip() on it raises on EVERY chat request, and coercing it with str()
    # sends its Python repr to the model as the deployment's system prompt,
    # which nothing would ever surface. Ignoring it is the third option and
    # the right one: it degrades to exactly the documented contract for an
    # absent row, and the boot line the reconcile logs still shows what was
    # written. An absent or blank row means "not configured" and leaves the
    # payload untouched for the same reason.
    _hive_chat_prompt_row = await Config.get({CONFIG_KEY!r})
    _hive_chat_system_prompt = (
        _hive_chat_prompt_row.strip()
        if isinstance(_hive_chat_prompt_row, str)
        else ''
    )
    if _hive_chat_system_prompt:
        # Default append=False, which PREPENDS. Hive's block first, the user's
        # own system text after it.
        form_data['messages'] = add_or_update_system_message(
            _hive_chat_system_prompt,
            form_data.get('messages', []),
        )

"""

text = TARGET.read_text()

assert MARKER not in text, f"{MARKER} is already present -- patch applied twice"

assert text.count(SIGNATURE) == 1, (
    "process_chat_payload is not defined exactly once -- upstream open-webui "
    "source shifted, patch needs updating"
)
assert text.count(ANCHOR) == 1, (
    "the pre-RAG snapshot comment anchor is not present exactly once -- "
    "upstream open-webui source shifted, patch needs updating"
)
assert text.count(SNAPSHOT) == 1, (
    "metadata['system_prompt'] is not assigned exactly once -- upstream "
    "open-webui source shifted, patch needs updating"
)

# The anchor has to belong to process_chat_payload's own body, not to some
# other function that happens to carry a similar comment.
sig_start = text.index(SIGNATURE)
body_start = sig_start + len(SIGNATURE)
next_top_level = re.search(r"\n\S", text[body_start:])
body_end = body_start + next_top_level.start() if next_top_level else len(text)
body = text[body_start:body_end]
assert ANCHOR in body, (
    "the pre-RAG snapshot anchor is not inside process_chat_payload's own body "
    "-- upstream open-webui source shifted, patch needs updating"
)
assert SNAPSHOT in body, (
    "the metadata['system_prompt'] assignment is not inside "
    "process_chat_payload's own body -- upstream open-webui source shifted"
)
assert body.index(ANCHOR) < body.index(SNAPSHOT), (
    "the anchor no longer precedes the metadata['system_prompt'] snapshot, so "
    "inserting here would no longer put the prompt inside that snapshot and it "
    "would be dropped after the first tool call of every turn"
)

# The reason the ordering above matters at all. If upstream stops restoring the
# snapshot, this assertion is the place that says so, rather than a comment
# claiming a constraint that no longer exists.
assert RESTORE in text, (
    "the native tool-call loop no longer restores metadata['system_prompt'], so "
    "the ordering constraint this patch is built around has changed. Re-read "
    "the tool-call branch before relaxing anything here"
)

# Names the inserted block closes over.
assert "from open_webui.models.config import Config\n" in text, (
    "middleware.py no longer imports Config -- patch needs updating"
)
assert "    add_or_update_system_message,\n" in text, (
    "middleware.py no longer imports add_or_update_system_message -- patch "
    "needs updating"
)

patched = text.replace(ANCHOR, INSERT + ANCHOR, 1)
ast.parse(patched)  # never write a middleware.py that cannot be imported
TARGET.write_text(patched)
