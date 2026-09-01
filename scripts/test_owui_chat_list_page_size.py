#!/usr/bin/env python3
"""Keep the sidebar's page-size constant honest against the backend (issue #1625).

`Sidebar.svelte` decides the chat list has reached its end when a page comes
back SHORT, which is what spares an account below one page the empty page 2
request the issue was filed about. That test needs a number, and the number
lives in the pinned upstream backend
(`routers/chats.py.get_session_user_chat_list`, `limit = 60`).

Drift in one direction is harmless: if the backend page grows, a full page
reads as short, the sidebar stops early once and the next scroll asks again.
Drift in the OTHER direction is not. If the backend page shrinks below the
constant, every full page reads as short, pagination stops after page 1 and an
account with more conversations than one page silently loses the rest of its
list, which is the same blank-nav symptom #1625 was about, arrived at from the
opposite side.

An upstream bump is the realistic way that happens, and nothing else in this
repository would notice. So this asserts the two agree, and fails loudly when
they stop agreeing rather than leaving a truncated list to be discovered by a
customer.

Run: python3 scripts/test_owui_chat_list_page_size.py
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SIDEBAR = ROOT / "vendor/open-webui/src/lib/components/layout/Sidebar.svelte"
BACKEND = ROOT / "vendor/open-webui/backend/open_webui/routers/chats.py"

HANDLER = "async def get_session_user_chat_list("


def backend_page_size() -> int:
    source = BACKEND.read_text(encoding="utf-8")
    start = source.find(HANDLER)
    assert start != -1, f"{BACKEND} no longer defines {HANDLER!r}"
    # The handler ends at the next top-level decorator or def.
    rest = source[start + len(HANDLER) :]
    end = rest.find("\n@router.")
    body = rest if end == -1 else rest[:end]
    limits = re.findall(r"^\s+limit = (\d+)$", body, flags=re.MULTILINE)
    assert len(limits) == 1, (
        f"expected exactly one `limit = <n>` inside get_session_user_chat_list, "
        f"found {limits}. The sidebar's short-page test needs one unambiguous "
        f"page size; if the handler now has several, teach this check which one "
        f"the paged branch uses rather than deleting it."
    )
    return int(limits[0])


def sidebar_page_size() -> int:
    source = SIDEBAR.read_text(encoding="utf-8")
    found = re.findall(r"^\tconst CHAT_LIST_PAGE_SIZE = (\d+);$", source, flags=re.MULTILINE)
    assert len(found) == 1, (
        f"expected exactly one CHAT_LIST_PAGE_SIZE declaration in {SIDEBAR}, "
        f"found {found}"
    )
    return int(found[0])


def test_the_sidebar_knows_the_real_page_size() -> None:
    backend = backend_page_size()
    sidebar = sidebar_page_size()
    assert sidebar == backend, (
        f"Sidebar.svelte's CHAT_LIST_PAGE_SIZE is {sidebar} but "
        f"routers/chats.py serves {backend} chats per page. While the sidebar's "
        f"number is the LARGER of the two, every full page reads as a short one "
        f"and the chat list stops loading after page 1."
    )


def test_the_short_page_test_is_still_the_thing_being_pinned() -> None:
    """The constant is only load bearing while something compares against it.
    Deleting the comparison and leaving the constant would leave this check
    passing over a sidebar that had gone back to the empty-page test."""
    source = SIDEBAR.read_text(encoding="utf-8")
    for fragment in (
        "allChatsLoaded = _chats.length < CHAT_LIST_PAGE_SIZE",
        "allChatsLoaded = newChatList.length < CHAT_LIST_PAGE_SIZE",
    ):
        assert fragment in source, f"{SIDEBAR} no longer contains {fragment!r}"


def main() -> int:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui chat list page size agrees with the backend (issue #1625)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
