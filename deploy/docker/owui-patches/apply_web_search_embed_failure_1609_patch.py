"""Build-time splice: stop web search reporting success after it dropped
every source it fetched (issue #1609).

The defect, reproduced live on the demo box three times on 2026-08-31. Five
pages were fetched, every one of them indexed into nothing, and the assistant
then told the user it had no live internet access. From the container's log:

    Fetching pages: 5/5
    429, message='Too Many Requests', url='http://edge-api:8080/v1/embeddings'
    query_doc:result [[]] [[]]

`process_web_search` wraps its `save_docs_to_vector_db` call in a bare
`except Exception as e: log.debug(...)` and then returns `status: True` with
the collection name it never wrote. So the one failure that empties the
collection is reported to the caller as a completed search, the middleware
emits its "Searched 5 sites" status, retrieval finds nothing in a collection
that does not exist, and the model answers from its own knowledge. The
confident denial is what made this demo-breaking rather than merely broken,
and `log.debug` is what let it survive: nothing above the caller ever saw a
failure.

Nothing partial is lost by failing here. `save_docs_to_vector_db` embeds every
chunk before it inserts anything, so if that call raises then no chunk was
written and the collection is empty by construction. Raising converts a
guaranteed-useless empty retrieval into the visible "An error occurred while
searching the web" status the middleware already emits on its own except
branch (utils/middleware.py, chat_web_search_handler).

Two things that assertion depends on, named rather than assumed silently.

First, the embed-before-insert ordering is upstream's, not a guarantee we
hold. It is true of the pinned digest and nothing in this build asserts it, so
a future digest bump that started inserting per batch would turn this raise
into a silent discard of whatever had already been written. Not worth a guard
today; worth knowing before the next bump.

Second, "fatal to this search" is not "the turn stops", and the two callers
differ. The frontend calls this endpoint directly (`processWebSearch`,
src/lib/apis/retrieval/index.ts, imported by Chat.svelte), so a client-side
search receives the 502 and this detail string, which is why the no-exception
-text decision below is load bearing rather than merely defensive. On the
middleware path the detail is discarded: `chat_web_search_handler` catches
everything, emits its own "An error occurred while searching the web" status,
returns `form_data`, and the turn continues. So a chat-path failure is a
visible red error next to an answer with no sources, and the model may still
answer from its own knowledge. That is the intended fix for the reported
defect, which was the claim of success, not the answer itself.

The raised detail carries no exception text, deliberately. The exception here
is aiohttp's, and aiohttp bakes the request URL into its string, so re-raising
the original would publish `http://edge-api:8080/v1/embeddings` to the
signed-in user through `ERROR_MESSAGES.DEFAULT(e, ...)` in the enclosing
handler -- the exact leak apply_audio_error_leak_patch.py exists to close.
The operator still gets the whole cause, at ERROR level, in the container log.

Applied here rather than in vendor/open-webui because the chat image builds
only the FRONTEND from the vendored tree and takes the backend from the pinned
upstream image (see Dockerfile.open-webui), so a backend edit under vendor/ is
inert. The exact pinned literal is asserted below: a digest bump that moves it
fails the image build loudly instead of silently restoring the swallow.
"""

import ast
import os
import pathlib

TARGET = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_RETRIEVAL_PY",
        "/app/backend/open_webui/routers/retrieval.py",
    )
)

MARKER = "# hive (#1609)"

OLD = """            except Exception as e:
                log.debug(f'error saving docs: {e}')
"""

NEW = """            except Exception:
                # hive (#1609): loud, and fatal to this search. The swallow
                # here returned `status: True` and a collection name nothing
                # had been written to, so a search that dropped all five of
                # its fetched pages was indistinguishable from one that
                # worked, and the model answered that it had no internet
                # access seconds after five real pages were fetched.
                #
                # Nothing partial is lost: save_docs_to_vector_db embeds every
                # chunk before inserting any of them, so a raise here means
                # the collection is empty. That ordering is upstream's, not a
                # guarantee, so re-check it on a digest bump.
                #
                # Fatal to this SEARCH, not to the turn. A client-side call
                # (processWebSearch) receives this 502 and this detail. On the
                # chat path chat_web_search_handler catches it, emits its own
                # "An error occurred while searching the web" status and lets
                # the turn continue, so the user sees a visible error next to
                # an answer with no sources rather than no answer.
                #
                # The detail is written here rather than borrowed from the
                # exception: aiohttp bakes the request URL into its own
                # string, so `ERROR_MESSAGES.DEFAULT(e, ...)` below would
                # publish an internal compose address to the signed-in user
                # (the leak apply_audio_error_leak_patch.py closes). The
                # operator gets the full cause from the log line above.
                log.exception('hive (#1609): web search indexing failed, no sources attached')
                raise HTTPException(
                    status.HTTP_502_BAD_GATEWAY,
                    detail='Web search fetched pages but could not index them, so no sources were attached to this answer. Please try again.',
                ) from None
"""

text = TARGET.read_text()

assert text.count(OLD) == 1, (
    "the save_docs_to_vector_db swallow in process_web_search is not present "
    "exactly once -- upstream open-webui source shifted, patch needs updating"
)
assert text.count(MARKER) == 0, (
    "the hive (#1609) marker is already present -- patch applied twice, or the "
    "pinned image already carries it"
)

# Names the replacement closes over. Both are module-level imports in the
# pinned file and are used by every other raise in this router, but an upstream
# reshuffle that dropped either would turn this splice into a NameError on the
# one path nobody exercises until a search fails.
assert "from fastapi import (" in text or "from fastapi import" in text, (
    "retrieval.py no longer imports from fastapi -- patch needs updating"
)
assert "HTTPException" in text, (
    "retrieval.py no longer references HTTPException -- patch needs updating"
)
assert "status.HTTP_404_NOT_FOUND" in text, (
    "retrieval.py no longer uses the fastapi `status` module -- patch needs "
    "updating"
)

patched = text.replace(OLD, NEW, 1)
assert patched.count(MARKER) == 1, "the marker was not inserted exactly once"
ast.parse(patched)  # never write a router module that cannot be imported
TARGET.write_text(patched)
