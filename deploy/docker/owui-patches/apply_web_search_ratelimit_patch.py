"""Build-time splice: make DuckDuckGo rate limits loud.

The pinned image's search_duckduckgo catches RatelimitException, logs it, and
returns an empty list, so a rate-limited search is indistinguishable from "the
web has nothing": the native search_web builtin hands the model `[]`, and the
model answers "no results" with no signal that search itself is unavailable.
The legacy forced-search path swallows the same exception into its
"No search results found" status, which reads as a legitimate empty result set
rather than an outage.

Re-raising lands the exception in the two handlers that DO surface it:
the search_web builtin returns {'error': ...} to the model (which can then
tell the user search is unavailable), and the legacy path's except branch
emits its visible "An error occurred while searching the web" status event.

Asserts the exact pinned literals so a future digest bump whose engine wrapper
shifted breaks the build loudly instead of silently reverting to the silent
swallow.
"""

import ast
import pathlib

TARGET = pathlib.Path("/app/backend/open_webui/retrieval/web/duckduckgo.py")

OLD = """        except RatelimitException as e:
            log.error(f'RatelimitException: {e}')
            search_results = []
"""

NEW = """        except RatelimitException:
            # hive: a rate limit is an error, not an empty result set. The
            # swallow above made a rate-limited search indistinguishable from
            # "the web has nothing": the native search_web builtin returned []
            # and the model answered "no results" with no signal that search
            # itself was unavailable. Re-raise so the builtin's except returns
            # an error payload the model can see and relay, and so the legacy
            # forced-search path lands in its own except branch, which emits a
            # visible "An error occurred while searching the web" status.
            raise
"""

text = TARGET.read_text()

assert text.count(OLD) == 1, (
    "the RatelimitException swallow block is not present exactly once -- "
    "upstream open-webui source shifted, patch needs updating"
)

patched = text.replace(OLD, NEW, 1)
ast.parse(patched)  # never write an engine module that cannot be imported
TARGET.write_text(patched)
