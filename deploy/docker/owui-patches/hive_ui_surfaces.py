"""Remove the Open WebUI chat surfaces that have no Hive counterpart (#772).

Hive Chat is sold as a Claude-Enterprise-shaped product. Open WebUI 0.10.2
ships several stock surfaces that are not part of it: a second, instance-wide
model registry (Workspace > Models), Workspace > Prompts, Workspace > Tools,
the admin Playground, the vendor's own Documentation and Releases links in the
user menu, and its release-notes dialog, which greets a new signed-in owner
with a "What's New in Hive" banner over another vendor's changelog. Claude
removes outright whatever is never a user decision and greys out only what a
user could plausibly be granted, so all of these are removed rather than
disabled.

Workspace > Knowledge is deliberately untouched. It is the document upload
behind Hive's RAG surface and our analogue of Claude Projects.

Why this is a build-time rewrite of the compiled bundle and not configuration:

* Every one of these gates is written `role === 'admin' || <permission>`
  (src/lib/components/layout/Sidebar.svelte, Sidebar/UserMenu.svelte and
  src/routes/(app)/workspace/+layout.svelte at v0.10.2), and the Hive tenant
  role patch promotes every tenant owner to Open WebUI `admin`. A permission
  based hide therefore hides nothing from the people who actually use the
  deployment. `USER_PERMISSIONS_WORKSPACE_*` cannot reach these gates at all.
* Open WebUI 0.10.2 has no environment variable for any of them. The
  `ENABLE_PLUGINS=false` recipe quoted by third-party Open WebUI hardening
  guides does not exist in this release: the name appears nowhere in the
  pinned image's backend, so setting it is a no-op (the same way
  `ENABLE_MODEL_FILTER` and `ENABLE_ADMIN_PANEL` were no-ops here). Even if it
  existed, a switch that removes "the workspace interface" wholesale would
  take Knowledge with it, which this deployment needs.
* Notes, Calendar and Automations are the exception and are NOT handled here:
  those three do have real feature flags (`ENABLE_NOTES`, `ENABLE_CALENDAR`,
  `ENABLE_AUTOMATIONS`) whose gates are `$config.features.enable_*` AND the
  role check, so turning the flag off hides them from admins too. They are
  persisted config, so docker-compose.yml sets them and
  hive_rag_env_config.py reconciles them onto an already-booted database.

The image ships a prebuilt, minified SvelteKit bundle and no frontend source,
so each rewrite below is a verbatim substring of that bundle. That makes them
tied to one image digest by construction, which is the intent: apply_ui_
surfaces_patch.py asserts every rewrite matched its exact expected count and
that the guard strings survived, so a digest bump fails the build loudly
instead of silently restoring a removed surface. Each entry records the
upstream source it compiles from so the next person can re-derive it.

The table also carries a second, smaller job (#833): the accessibility names
that Open WebUI's chat sidebar toggle and its neighbouring navbar controls do
not have. Those rewrites remove nothing and add only attributes, and they are
grouped at the end of REWRITES under their own comment.

ponytail: exact strings, no minified-AST parser. A parser would survive a
digest bump that these strings do not, but it would also be the thing nobody
can review, and a loud build failure is the cheaper outcome.
"""

from dataclasses import dataclass


@dataclass(frozen=True)
class Rewrite:
    """One removed surface, as a literal substitution over the built bundle."""

    surface: str
    upstream: str
    find: str
    replace: str
    count: int = 1


# `!1` is the minifier's own spelling of `false`. Replacing the whole condition
# (rather than deleting the block) keeps each Svelte if-block structurally
# intact while guaranteeing its branch never renders.
REWRITES = (
    Rewrite(
        surface="workspace-models-tab",
        upstream="src/routes/(app)/workspace/+layout.svelte, the {#if ...workspace?.models} tab",
        find=(
            '(((e=o())==null?void 0:e.role)==="admin"||(t=(r=(a=o())==null?void 0:'
            'a.permissions)==null?void 0:r.workspace)!=null&&t.models)&&s(U)'
        ),
        replace="!1&&s(U)",
    ),
    Rewrite(
        surface="workspace-prompts-tab",
        upstream="src/routes/(app)/workspace/+layout.svelte, the {#if ...workspace?.prompts} tab",
        find=(
            '(((e=o())==null?void 0:e.role)==="admin"||(t=(r=(a=o())==null?void 0:'
            'a.permissions)==null?void 0:r.workspace)!=null&&t.prompts)&&s(P)'
        ),
        replace="!1&&s(P)",
    ),
    Rewrite(
        surface="workspace-tools-tab",
        upstream="src/routes/(app)/workspace/+layout.svelte, the {#if ...workspace?.tools} tab",
        find=(
            '(((e=o())==null?void 0:e.role)==="admin"||(t=(r=(a=o())==null?void 0:'
            'a.permissions)==null?void 0:r.workspace)!=null&&t.tools)&&s(ae)'
        ),
        replace="!1&&s(ae)",
    ),
    Rewrite(
        # #783. Same gate shape and same reasoning as the three above: an empty
        # but fully writable admin surface ("No skills found", "+ New Skill",
        # "Import") with no Hive product behind it. It was kept out of the
        # first pass on the theory that Claude Enterprise ships Agent Skills,
        # so it had a counterpart; it does not on this deployment, where the
        # tab is the only thing that exists.
        surface="workspace-skills-tab",
        upstream="src/routes/(app)/workspace/+layout.svelte, the {#if ...workspace?.skills} tab",
        find=(
            '(((e=o())==null?void 0:e.role)==="admin"||(t=(r=(a=o())==null?void 0:'
            'a.permissions)==null?void 0:r.workspace)!=null&&t.skills)&&s(j)'
        ),
        replace="!1&&s(j)",
    ),
    Rewrite(
        # /workspace itself is an index that redirects to the first surface the
        # session may see, and its admin branch redirects to /workspace/models.
        # With Models gone that lands an admin on a page reachable from nowhere,
        # so the index becomes an unconditional redirect to Knowledge, the one
        # workspace surface Hive keeps. A session without knowledge permission
        # is then bounced to / by +layout.svelte's own onMount guard, which is
        # unchanged.
        surface="workspace-index-redirect",
        upstream="src/routes/(app)/workspace/+page.svelte, the whole onMount redirect chain",
        find=(
            'x(()=>{var e,r,p,i,a,t,m,l,n,c,k,w,f,u,$,d;((e=o())==null?void 0:e.role)!=="admin"?'
            '(i=(p=(r=o())==null?void 0:r.permissions)==null?void 0:p.workspace)!=null&&i.models?'
            's("/workspace/models"):'
            '(m=(t=(a=o())==null?void 0:a.permissions)==null?void 0:t.workspace)!=null&&m.knowledge?'
            's("/workspace/knowledge"):'
            '(c=(n=(l=o())==null?void 0:l.permissions)==null?void 0:n.workspace)!=null&&c.prompts?'
            's("/workspace/prompts"):'
            '(f=(w=(k=o())==null?void 0:k.permissions)==null?void 0:w.workspace)!=null&&f.tools?'
            's("/workspace/tools"):'
            '(d=($=(u=o())==null?void 0:u.permissions)==null?void 0:$.workspace)!=null&&d.skills?'
            's("/workspace/skills"):s("/"):s("/workspace/models")})'
        ),
        replace='x(()=>{s("/workspace/knowledge")})',
    ),
    Rewrite(
        surface="sidebar-playground-item",
        upstream="src/lib/components/layout/Sidebar.svelte, isMenuItemVisible case 'playground'",
        find='case"playground":return((sr=r())==null?void 0:sr.role)==="admin";',
        replace='case"playground":return!1;',
    ),
    Rewrite(
        # Distinguished from the Admin Panel entry, which compiles to the same
        # shape in the same chunk, only by the branch identifiers. That entry is
        # asserted intact by GUARDS below.
        surface="usermenu-playground-item",
        upstream="src/lib/components/layout/Sidebar/UserMenu.svelte, the {#if role === 'admin'} wrapping /playground",
        find='T(kt,_=>{q()==="admin"&&_(Kt)})',
        replace="T(kt,_=>{!1&&_(Kt)})",
    ),
    Rewrite(
        # Open WebUI's release-notes dialog. It opens itself on first load
        # whenever `$settings.version !== $config.version`
        # (src/routes/(app)/+layout.svelte), under a `role === 'admin'` gate
        # every tenant owner passes, and renders the CHANGELOG.md baked into
        # the image (backend/open_webui/env.py parses it at import time and
        # serves it from /api/changelog). It is titled with WEBUI_NAME, so a
        # Hive buyer's first screen is a dialog headed "What's New in Hive"
        # listing another vendor's features and commit ids.
        #
        # v0.10.2 exposes no switch for it: no environment variable and no
        # persisted config key touches it anywhere in the pinned image. The
        # only controls are a per-user setting (`showChangelog`, Settings >
        # Interface, default on) and the dialog's own close button, which
        # merely writes the running version into that user's settings. That
        # last one is why this is patched at the render site instead: anything
        # that works by recording the current version comes back at the next
        # version, so an image bump would re-open it for every user. Gating the
        # single place the dialog is rendered also covers its two manual
        # openers (Settings > About and Admin Settings > General, both
        # `showChangelog.set(true)`), which are left in place but inert. An
        # operator-owned release-notes feed is tracked separately.
        surface="changelog-modal",
        upstream="src/routes/(app)/+layout.svelte, <ChangelogModal bind:show={$showChangelog} />",
        find="j1(ee,{get show(){return Js(),W()},set show(p){Ys(qs,p)},$$legacy:!0})",
        replace="j1(ee,{get show(){return !1},set show(p){Ys(qs,p)},$$legacy:!0})",
    ),
    Rewrite(
        # One gate wraps both anchors, so this removes Documentation and
        # Releases together.
        surface="usermenu-vendor-links",
        upstream="src/lib/components/layout/Sidebar/UserMenu.svelte, the help block's docs.openwebui.com + open-webui releases links",
        find='T(n,g=>{i(),r(()=>{var R;return((R=i())==null?void 0:R.role)==="admin"})&&g(y)})',
        replace="T(n,g=>{!1&&g(y)})",
    ),
    Rewrite(
        # #784. Settings > About is reachable by every signed-in user, not just
        # admins, and it is the one screen the white-label pass stopped short
        # of: the version string above it already reads "Hive Version", then
        # the block underneath credits Open WebUI Inc. and links to the vendor.
        #
        # Node structure is preserved exactly. Svelte 5 walks this compiled
        # template positionally (`we=a(ie)` takes the <pre>'s first text node,
        # then `wr(4)` skips the two anchors and the whitespace between them),
        # so removing an element here would desynchronise the walker and blank
        # the tab. Both anchors therefore stay; only their text and href
        # change. The runtime still writes "Copyright (c) <year> " into that
        # first text node, so the line renders as Hive's own.
        #
        # The Twemoji CC-BY 4.0 credit two lines above is deliberately left
        # alone: it is a licence obligation attached to an asset, not vendor
        # branding. Whether the upstream notice may be replaced rather than
        # retained is the same clause 4 question this Dockerfile's header
        # already records the owner's decision on, under the <=50 user
        # carve-out. Re-check with counsel before any rollout past it.
        surface="about-vendor-copyright",
        upstream="src/lib/components/chat/Settings/About.svelte, the copyright <pre>",
        find=(
            '<div><pre class="text-xs text-gray-400 dark:text-gray-500"> '
            '<a href="https://openwebui.com" target="_blank" class="underline">Open WebUI Inc.</a> '
            '<a href="https://github.com/open-webui/open-webui/blob/main/LICENSE" '
            'target="_blank">All rights reserved.</a>\n</pre></div>'
        ),
        replace=(
            '<div><pre class="text-xs text-gray-400 dark:text-gray-500"> '
            '<a href="/" class="underline">Hive</a> '
            '<a href="/">All rights reserved.</a>\n</pre></div>'
        ),
    ),
    Rewrite(
        # #784, second half. The creator credit is removed rather than
        # rebranded: nobody is credited on this screen at all. Same positional
        # constraint as above, so the anchor element survives with empty text
        # (`p=a(N)` is its preceding text node, `wr()` skips the anchor), and
        # the label that fills that text node is emptied by the next rewrite.
        # Without both, the line renders a bare "Created by".
        surface="about-creator-link",
        upstream="src/lib/components/chat/Settings/About.svelte, the tjbck credit anchor",
        find=(
            '<div class="mt-2 text-xs text-gray-400 dark:text-gray-500"> '
            '<a class=" text-gray-500 dark:text-gray-300 font-medium" '
            'href="https://github.com/tjbck" target="_blank">Timothy J. Baek</a></div>'
        ),
        replace=(
            '<div class="mt-2 text-xs text-gray-400 dark:text-gray-500"> '
            '<a class=" text-gray-500 dark:text-gray-300 font-medium" href="/"></a></div>'
        ),
    ),
    Rewrite(
        # #784, third half, found by looking at the screen rather than at the
        # issue text: the same tab carries a row of vendor social badges
        # (Discord "Open WebUI", Follow @OpenWebUI, Star us on Github) that the
        # issue's quoted block skipped. Removing the copyright while leaving
        # these would have shipped a white-label pass whose own proof
        # screenshot still said Open WebUI three times.
        #
        # This one may be emptied rather than merely retargeted because the
        # template is instantiated and inserted whole (`D=P=>{var B=mm();
        # i(P,B)}`) and never walked into, so no positional traversal depends
        # on its children. The outer div stays for exactly that reason: the
        # identifier and the call site must keep their shape.
        #
        # Only the copy in nodes/*.js that Settings > About uses. Admin
        # Settings carries the same markup in its own chunk and is left alone,
        # because /admin is already 404'd at the proxy and its template is
        # walked positionally.
        surface="about-vendor-social-badges",
        upstream="src/lib/components/chat/Settings/About.svelte, the Discord/X/GitHub badge row",
        find=(
            """mm=g('<div class="flex space-x-1"><a href="https://discord.gg/5rJgQTnV4s" """
            """target="_blank"><img alt="Discord" src="https://img.shields.io/badge/"""
            """Discord-Open_WebUI-blue?logo=discord&amp;logoColor=white"/></a> """
            """<a href="https://twitter.com/OpenWebUI" target="_blank"><img """
            """alt="X (formerly Twitter) Follow" src="https://img.shields.io/twitter/"""
            """follow/OpenWebUI"/></a> <a href="https://github.com/open-webui/open-webui" """
            """target="_blank"><img alt="Github Repo" src="https://img.shields.io/github/"""
            """stars/open-webui/open-webui?style=social&amp;label=Star us on Github"/>"""
            """</a></div>')"""
        ),
        replace="""mm=g('<div class="flex space-x-1"></div>')""",
    ),
    Rewrite(
        surface="about-creator-label",
        upstream='src/lib/components/chat/Settings/About.svelte, the $i18n.t("Created by") label',
        find=',()=>l().t("Created by")]',
        replace=',()=>""]',
    ),
    # ----------------------------------------------------------------- #833
    # Accessibility, not removal. Every entry above takes a surface away; the
    # six below leave every surface exactly where it is and only add
    # attributes. test_the_accessibility_rewrites_only_add_attributes proves
    # that byte for byte: strip the added attributes back out of `replace` and
    # what is left must equal `find`, so none of these can move a node, change
    # a class or touch a handler.
    #
    # The control that opens the chat sidebar had no accessible name in either
    # layout, so a screen reader announced it as a bare "button" with no
    # indication of what it opens. That is the defect. It also left the
    # sidebar impossible to pin open from an automated pass, which is why four
    # chat surfaces sit outside the coverage denominator.
    #
    # Open WebUI 0.10.2 writes the names of the neighbouring controls as
    # static attributes in these same templates (aria-label="New Chat",
    # aria-label="Controls", and aria-label="Chat Menu" on the sidebar's own
    # chat rows), so these follow that convention rather than introducing a
    # second, runtime-translated naming mechanism into markup that has none.
    # The tooltips beside them stay translated exactly as before.
    Rewrite(
        # The collapsed rail on a wide viewport. The whole rail column is one
        # <button> and it carries the click handler, and that button was
        # nameless. The small logo button nested inside it is the one upstream
        # labelled, and it has no handler of its own (its clicks reach the
        # sidebar only by bubbling out to this element), so the control a
        # keyboard or screen reader user actually operates is this one. It
        # renders only under `!$mobile && !$showSidebar`, which is what makes
        # the collapsed state safe to state as a literal.
        surface="sidebar-toggle-rail",
        upstream="src/lib/components/layout/Sidebar.svelte, the collapsed rail's outer <button>",
        find='id="sidebar"><button><div class="pb-1.5">',
        replace=(
            'id="sidebar"><button aria-label="Open Sidebar" aria-expanded="false">'
            '<div class="pb-1.5">'
        ),
    ),
    Rewrite(
        # The same control on a narrow viewport, where the rail is not rendered
        # at all and this navbar button is the only way to open the sidebar.
        # Nothing named it, so on a phone the page carried no named sidebar
        # control anywhere. Rendered only under `$mobile && !$showSidebar`, so
        # again the state is a literal.
        surface="sidebar-toggle-navbar",
        upstream=(
            "src/lib/components/chat/Navbar.svelte, "
            "the {#if showSidebarToggle && !$showSidebar} button"
        ),
        find=(
            '<button class=" cursor-pointer flex rounded-lg hover:bg-gray-100 '
            'dark:hover:bg-gray-850 transition"><div class=" self-center p-1.5">'
            "<!></div></button>"
        ),
        replace=(
            '<button class=" cursor-pointer flex rounded-lg hover:bg-gray-100 '
            'dark:hover:bg-gray-850 transition" aria-label="Open Sidebar" '
            'aria-expanded="false"><div class=" self-center p-1.5"><!></div></button>'
        ),
    ),
    Rewrite(
        # The other half of the disclosure: the collapse button in the expanded
        # sidebar's header. Upstream already names this one (it binds
        # aria-label to the translated "Close Sidebar"), so only the state was
        # missing. Its whole subtree renders under `$showSidebar`, so "true"
        # holds for as long as the element exists. Without it a screen reader
        # could hear that the sidebar was collapsed but never that it opened.
        surface="sidebar-toggle-expanded-state",
        upstream=(
            "src/lib/components/layout/Sidebar.svelte, "
            "the collapse button in the expanded sidebar header"
        ),
        find='<button><div class=" self-center p-1.5"><!></div></button>',
        replace='<button aria-expanded="true"><div class=" self-center p-1.5"><!></div></button>',
    ),
    Rewrite(
        # The three below are the toggle's immediate siblings in the same
        # navbar cluster, nameless for the same reason: their label lived in a
        # tooltip, which is not an accessible name. New Chat and Controls sit
        # in that same row and were already labelled upstream, which is where
        # the wording and the form of these three come from. Scope stops at
        # this cluster.
        surface="navbar-temporary-chat-button",
        upstream="src/lib/components/chat/Navbar.svelte, the Temporary Chat toggle",
        find=(
            '<button class="flex cursor-pointer px-2 py-2 rounded-xl hover:bg-gray-50 '
            'dark:hover:bg-gray-850 transition" id="temporary-chat-button">'
        ),
        replace=(
            '<button class="flex cursor-pointer px-2 py-2 rounded-xl hover:bg-gray-50 '
            'dark:hover:bg-gray-850 transition" id="temporary-chat-button" '
            'aria-label="Temporary Chat">'
        ),
    ),
    Rewrite(
        surface="navbar-save-chat-button",
        upstream="src/lib/components/chat/Navbar.svelte, the Save Chat button",
        find=(
            '<button class="flex cursor-pointer px-2 py-2 rounded-xl hover:bg-gray-50 '
            'dark:hover:bg-gray-850 transition" id="save-temporary-chat-button">'
        ),
        replace=(
            '<button class="flex cursor-pointer px-2 py-2 rounded-xl hover:bg-gray-50 '
            'dark:hover:bg-gray-850 transition" id="save-temporary-chat-button" '
            'aria-label="Save Chat">'
        ),
    ),
    Rewrite(
        # "Chat Menu" is upstream's own name for the same menu on a sidebar
        # chat row, so this makes the navbar copy of it read identically.
        surface="navbar-chat-menu-button",
        upstream="src/lib/components/chat/Navbar.svelte, the chat context menu trigger",
        find=(
            '<button class="flex cursor-pointer px-2 py-2 rounded-xl hover:bg-gray-50 '
            'dark:hover:bg-gray-850 transition" id="chat-context-menu-button">'
        ),
        replace=(
            '<button class="flex cursor-pointer px-2 py-2 rounded-xl hover:bg-gray-50 '
            'dark:hover:bg-gray-850 transition" id="chat-context-menu-button" '
            'aria-label="Chat Menu">'
        ),
    ),
)


# Substrings that must survive untouched. These are the surfaces Hive keeps
# whose compiled shape is nearly identical to something being removed, so an
# over-broad rewrite would silently take them out. Checked after the rewrites.
GUARDS = (
    (
        "workspace-knowledge-tab",
        '(((e=o())==null?void 0:e.role)==="admin"||(t=(r=(a=o())==null?void 0:'
        'a.permissions)==null?void 0:r.workspace)!=null&&t.knowledge)&&s(O)',
    ),
    (
        "usermenu-admin-panel",
        'T(Ie,_=>{q()==="admin"&&_(De)})',
    ),
    (
        # Rendered from the same layout, one statement before the changelog
        # dialog and in the same shape. This is the Settings dialog, which the
        # user menu's Settings entry opens.
        "settings-modal",
        "P1(ge,{get show(){return Js(),w()},set show(p){Ys(Vs,p)},$$legacy:!0})",
    ),
)


def apply(text: str) -> tuple[str, dict]:
    """Apply every rewrite to one bundle file. Returns the text and a
    {surface: replacements_made} count for the caller to total up."""
    hits = {}
    for rewrite in REWRITES:
        found = text.count(rewrite.find)
        if found:
            text = text.replace(rewrite.find, rewrite.replace)
        hits[rewrite.surface] = found
    return text, hits


def verify_counts(totals: dict) -> list:
    """Failures for any rewrite that did not match its expected site count.

    Zero is the case that matters and the reason the caller must treat this as
    a hard build failure rather than a warning. A `find` that has drifted off
    the shipped bundle, because a digest bump moved it or because somebody
    edited the string, rewrites nothing at all, and a rewrite that rewrites
    nothing puts the surface it was supposed to remove straight back in front
    of the customer. Nothing else in the build would notice: the pass itself
    reports success, and the image ships.

    Lives here rather than inline in apply_ui_surfaces_patch.py so this
    failure path is reachable from a test. See
    test_the_build_patch_fails_when_a_rewrite_matches_nothing.
    """
    return [
        f"{rewrite.surface}: expected {rewrite.count} site(s), "
        f"found {totals.get(rewrite.surface, 0)} (upstream: {rewrite.upstream})"
        for rewrite in REWRITES
        if totals.get(rewrite.surface, 0) != rewrite.count
    ]
