"""Build-time removal of the Settings > Integrations tab from the Open WebUI SPA.

Why (#771): that tab is the only place the chat UI offers "Manage Tool Servers"
and "Open Terminal". Both take an arbitrary URL plus an auth credential and then
route tool calls or terminal sessions to it. The tool servers registered there
are `direct` ones: the browser calls the configured URL itself, so they leave
the Hive gateway's metering, billing, and sanitization entirely, and no proxy
rule on the chat origin can see them, let alone stop them. Removing the entry
point is the only lever that reaches that half.

Why a patch and not configuration: `SettingsModal.svelte` shows the tab when
`$user.role === 'admin' || ($user.role === 'user' && permissions.features
.direct_tool_servers)`. Every tenant owner on this deployment holds the Open
WebUI admin role (#748), so they take the first arm and
`direct_tool_servers: false` never applies to them. v0.10.2 has no config key
that removes the panel; `ENABLE_ADMIN_PANEL` does not exist in this version.

Why removing the tab descriptor rather than neutering the guard: the descriptor
is built out of source string literals (`id`, `title`, and the keyword list),
which survive minification unchanged, whereas the guard is an optional-chaining
expression over mangled locals that a rebuild would rename. Dropping the
descriptor closes every route into the panel at once, because the nav button,
the settings search, and the content pane are all driven from the filtered
descriptor list: `getAvailableSettings()` filters this array,
`setFilteredSettings()` narrows it by search, the nav `{#each}` renders only
what survives, and `selectedTab` is reset to `filteredSettings[0]` whenever it
names a tab that is not in the list, which is what disarms the one deep link
into it (`showSettings.set('tools')` from the message input's terminal menu).

Everything downstream is data-driven off empty stores and needs no patch of its
own. `TerminalMenu` renders only when a terminal server exists, direct ones can
no longer be added once this tab is gone, and the system list comes from
`GET /api/v1/terminals/`, which `Caddyfile.owui` now refuses on this origin;
its client returns `[]` on any non-2xx, so that path degrades to "no terminals"
rather than to an error.

Asserts its own effect and fails the build otherwise, the same posture as this
Dockerfile's other patches: a future open-webui digest bump whose bundle shifted
must break the build loudly instead of silently shipping the panel back.
"""

import pathlib
import re
import sys

NODES = pathlib.Path("/app/build/_app/immutable/nodes")

# The descriptor as the bundler emits it. Every character here comes from a
# source string literal in SettingsModal.svelte's `allSettings` array, so it is
# stable under minification in a way the surrounding code is not.
ENTRY = re.compile(r'\{id:"tools",title:"Integrations",keywords:\[[^\]]*\]\},')

# Siblings that must survive, proving the array itself was not damaged and that
# this really is the settings-tab list rather than some other object literal.
SIBLINGS = ('{id:"connections",title:"Connections"', '{id:"personalization",title:"Personalization"')


def main() -> int:
    if not NODES.is_dir():
        print(f"{NODES} is missing: open-webui's build layout changed", file=sys.stderr)
        return 1

    hits = []
    for path in sorted(NODES.glob("*.js")):
        text = path.read_text(encoding="utf-8")
        count = len(ENTRY.findall(text))
        if count:
            hits.append((path, text, count))

    if len(hits) != 1 or hits[0][2] != 1:
        found = ", ".join(f"{p.name} x{c}" for p, _, c in hits) or "none"
        print(
            "expected the Integrations settings-tab descriptor exactly once across "
            f"{NODES}, found: {found}. Upstream open-webui's Settings modal shifted; "
            "update this patch deliberately rather than shipping the panel.",
            file=sys.stderr,
        )
        return 1

    path, text, _ = hits[0]
    for sibling in SIBLINGS:
        if sibling not in text:
            print(
                f"{path.name} does not contain {sibling}, so this is not the settings "
                "tab list this patch was written against",
                file=sys.stderr,
            )
            return 1

    patched = ENTRY.sub("", text, count=1)

    # The trailing comma is consumed with the entry. If upstream ever moves this
    # tab to the end of the array there would be no trailing comma, the regex
    # would not match at all, and the count check above would already have
    # failed. Guard the other direction too: never leave an array hole, which
    # would put `undefined` through `tab.id` and break the whole modal.
    if ",," in patched or "[," in patched:
        print(
            f"{path.name}: removing the descriptor left an array hole; refusing to write",
            file=sys.stderr,
        )
        return 1

    path.write_text(patched, encoding="utf-8")
    if ENTRY.search(path.read_text(encoding="utf-8")):
        print(f"{path.name}: descriptor still present after write", file=sys.stderr)
        return 1

    print(f"Settings > Integrations tab removed from {path.name}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
