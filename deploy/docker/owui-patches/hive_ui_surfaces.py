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
