#!/usr/bin/env python3
"""Self-check for the removal of the Open WebUI surfaces Hive does not ship (#772).

Every surface on the issue's list is removed by exactly one of three
mechanisms, and each of the three has a way of quietly coming undone:

* a bundle rewrite in deploy/docker/owui-patches/hive_ui_surfaces.py, which a
  future edit could weaken into a permission check that Open WebUI admins
  (i.e. every Hive tenant owner) bypass;
* a feature flag in deploy/docker/docker-compose.yml, which is persisted
  config and so also needs an entry in the reconcile, or it silently stops
  reaching any already-booted deployment;
* a path block in deploy/docker/Caddyfile.owui, so a typed URL cannot reach a
  route whose navigation entry is gone.

This file asserts all three against the repo's own files, plus the negative
half of each: Workspace > Knowledge, the Admin Panel entry, and Open WebUI's
own APIs must survive. No framework, no network, no Open WebUI import.
Run: python3 scripts/test_owui_ui_surfaces.py
"""
import asyncio
import importlib.util
import json
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
PATCHES = REPO / "deploy" / "docker" / "owui-patches"
COMPOSE = REPO / "deploy" / "docker" / "docker-compose.yml"
CADDYFILE = REPO / "deploy" / "docker" / "Caddyfile.owui"
DOCKERFILE = REPO / "deploy" / "docker" / "Dockerfile.open-webui"
EXCERPTS = PATCHES / "pinned-bundle-excerpts.json"


def _load(name: str):
    spec = importlib.util.spec_from_file_location(name, PATCHES / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


hive_ui_surfaces = _load("hive_ui_surfaces")
hive_rag_env_config = _load("hive_rag_env_config")


# The issue's own list. A surface named here must be covered by one of the
# three mechanisms; the coverage test below is what fails when it is not.
HIDDEN_SURFACES = {
    "workspace-models": "bundle",
    "workspace-prompts": "bundle",
    "workspace-skills": "bundle",
    "workspace-tools": "bundle",
    "playground": "bundle",
    "notes": "flag",
    "calendar": "flag",
    "automations": "flag",
    # 2026-08-17. Upstream's Memory feature, whose only surface here is the
    # Settings > Personalization tab. Nobody chose it: `ENABLE_MEMORIES`
    # defaults to true upstream, and while it is on, every chat turn this
    # deployment sends carries `features.memory: true` and pays for a lookup
    # against a second, Open WebUI owned memory store. Hive's memory subsystem
    # is one subsystem on Hive's own Postgres (.wolf/decisions.md D-020) and
    # does not exist yet, so this is not an early version of it, it is a
    # competing one.
    "memories": "flag",
    "vendor-links": "bundle",
    # 2026-08-17, both found by reading the Settings dialog this change was
    # already photographing: the Admin Panel entry's twin in the dialog's
    # bottom-left corner, which #909 left behind, and the vendor's own
    # translation-contribution link under the language picker.
    "settings-admin-link": "bundle",
    "settings-vendor-translate-link": "bundle",
    "changelog-modal": "bundle",
    "about-vendor-copyright": "bundle",
    "about-creator-credit": "bundle",
    "about-vendor-social": "bundle",
}

# The flag-backed half of the table above, as the environment variables that
# own it. Every one of these has to be stated in three places to actually be
# off everywhere: the image (a fresh or compose-less run), docker-compose.yml
# (the value an already-booted deployment reconciles from) and the reconcile
# map itself (without which the compose value never reaches a persisted row).
FLAG_VARIABLES = (
    "ENABLE_NOTES",
    "ENABLE_CALENDAR",
    "ENABLE_AUTOMATIONS",
    "ENABLE_MEMORIES",
)


def _rewrite(surface: str):
    return next(r for r in hive_ui_surfaces.REWRITES if r.surface == surface)


def _image_env() -> dict:
    """The ENABLE_* defaults Dockerfile.open-webui bakes into the image.

    Read out of the Dockerfile rather than out of a built image so this needs
    no Docker: PR CI never builds Dockerfile.open-webui (only owui-nightly.yml
    and deploy-demo-box.yml do), which is exactly the gap that let the image
    and the compose file disagree in the first place.
    """
    pairs = re.findall(
        r"^\s*(?:ENV\s+)?(ENABLE_\w+)=(\S+?)\s*\\?$", DOCKERFILE.read_text(), re.MULTILINE
    )
    return dict(pairs)


class FakeConfig:
    def __init__(self, stored: dict):
        self.stored = dict(stored)

    async def upsert(self, updates: dict) -> None:
        self.stored.update(updates)


# --------------------------------------------------------------------------
# 1. Bundle rewrites
# --------------------------------------------------------------------------

def test_every_rewrite_removes_its_surface_from_a_real_bundle_excerpt() -> None:
    """Each rewrite is exercised against a fragment carrying its own verbatim
    v0.10.2 target, so a rewrite that stopped matching, or whose replacement
    stopped disabling the branch, fails here rather than at image build time
    on somebody else's machine."""
    for rewrite in hive_ui_surfaces.REWRITES:
        excerpt = f"/*before*/{rewrite.find}/*after*/"
        patched, hits = hive_ui_surfaces.apply(excerpt)
        assert hits[rewrite.surface] == 1, (rewrite.surface, hits)
        assert rewrite.find not in patched, rewrite.surface
        assert patched == f"/*before*/{rewrite.replace}/*after*/", rewrite.surface
        # The surface must end up unconditionally hidden, not merely
        # permission-gated: Hive promotes every tenant owner to Open WebUI
        # admin, and every upstream gate reads `role === 'admin' || <perm>`.
        assert '==="admin"' not in rewrite.replace, rewrite.surface
        assert ".permissions" not in rewrite.replace, rewrite.surface


def test_each_rewrite_still_targets_its_own_surface() -> None:
    """A rewrite whose `find` was edited into something that matches nothing
    would restore its surface while every other assertion here still passed.
    apply_ui_surfaces_patch.py catches that against the real bundle at image
    build time; this catches the same edit in review, by pinning the marker
    each target must contain.

    Three user-menu entries carry no marker of their own: minification left
    them identifiable only by their branch identifiers, and all three compile
    to the very same shape (Playground, Admin Panel, and the vendor-links
    block). For those three the check is only that each `find` is distinct
    from the others and from any surviving GUARDS entry, asserted below.
    """
    markers = {
        "workspace-models-tab": (".models)", '==="admin"'),
        "workspace-prompts-tab": (".prompts)", '==="admin"'),
        "workspace-skills-tab": (".skills)", '==="admin"'),
        "workspace-tools-tab": (".tools)", '==="admin"'),
        "workspace-index-redirect": ('"/workspace/models"', '!=="admin"'),
        "sidebar-playground-item": ('case"playground"', '==="admin"'),
        "usermenu-playground-item": ('==="admin"',),
        "usermenu-admin-panel": ('==="admin"',),
        "usermenu-vendor-links": ('==="admin"',),
        "settings-admin-link": ('==="admin"',),
        "settings-vendor-translate-link": ('"en-US"', "license_metadata"),
        "changelog-modal": ("get show()",),
        "about-vendor-copyright": ("Open WebUI Inc.", "openwebui.com"),
        "about-creator-link": ("Timothy J. Baek", "github.com/tjbck"),
        "about-creator-label": ('"Created by"',),
        "about-vendor-social-badges": ("Star us on Github", "discord.gg"),
        # #833. These six identify themselves by the element they name, which
        # is also what a `find` edited into a guess would lose first.
        "sidebar-toggle-rail": ('id="sidebar">', "<button>"),
        "sidebar-toggle-navbar": ("<button", "rounded-lg"),
        "sidebar-toggle-expanded-state": ("<button>", 'class=" self-center p-1.5"'),
        "navbar-temporary-chat-button": ('id="temporary-chat-button"',),
        "navbar-save-chat-button": ('id="save-temporary-chat-button"',),
        "navbar-chat-menu-button": ('id="chat-context-menu-button"',),
    }
    guards = dict(hive_ui_surfaces.GUARDS)
    assert set(markers) == {r.surface for r in hive_ui_surfaces.REWRITES}, markers.keys()

    for rewrite in hive_ui_surfaces.REWRITES:
        for marker in markers[rewrite.surface]:
            assert marker in rewrite.find, (rewrite.surface, marker)
        for name, needle in guards.items():
            assert rewrite.find != needle, (rewrite.surface, name)

    finds = [rewrite.find for rewrite in hive_ui_surfaces.REWRITES]
    assert len(set(finds)) == len(finds), "two rewrites target the same site"


def test_rewrites_leave_the_surfaces_hive_keeps_alone() -> None:
    """Workspace > Knowledge and the Admin Panel entry compile to nearly the
    same shape as things being removed. An over-broad rewrite that swallowed
    them would take out the RAG document upload or lock admins out."""
    for name, needle in hive_ui_surfaces.GUARDS:
        patched, hits = hive_ui_surfaces.apply(f"/*before*/{needle}/*after*/")
        assert needle in patched, name
        assert not any(hits.values()), (name, hits)


def test_knowledge_is_not_named_by_any_rewrite() -> None:
    for rewrite in hive_ui_surfaces.REWRITES:
        if rewrite.surface == "workspace-index-redirect":
            # This one deliberately redirects TO Knowledge.
            assert '"/workspace/knowledge"' in rewrite.replace
            continue
        assert "knowledge" not in rewrite.find.lower(), rewrite.surface
        assert "knowledge" not in rewrite.replace.lower(), rewrite.surface


def test_the_changelog_dialog_cannot_return_on_the_next_image_bump() -> None:
    """Open WebUI opens its release-notes dialog whenever the version the user
    last acknowledged differs from the running one, so any fix that works by
    recording a version reopens the dialog at the next upgrade. This one gates
    the single site the dialog is rendered from, on a literal, so nothing about
    a version can bring it back and nothing that sets the store can either.
    """
    rewrite = next(r for r in hive_ui_surfaces.REWRITES if r.surface == "changelog-modal")
    assert "version" not in rewrite.replace.lower(), rewrite.replace
    assert "return !1" in rewrite.replace, rewrite.replace
    # It must gate the render, not a setter: a setter-level fix misses the two
    # manual openers in Settings > About and Admin Settings > General.
    assert "get show()" in rewrite.find and "get show()" in rewrite.replace, rewrite


def test_the_build_runs_the_bundle_patch() -> None:
    """A rewrite table nothing invokes removes nothing."""
    dockerfile = DOCKERFILE.read_text()
    assert "owui-patches/hive_ui_surfaces.py" in dockerfile, dockerfile[-400:]
    assert "python3 /tmp/apply_ui_surfaces_patch.py" in dockerfile, dockerfile[-400:]


def test_no_rewrite_puts_vendor_branding_back() -> None:
    """Every check above constrains what a rewrite removes; this one constrains
    what it writes. A replacement that keeps the vendor name or a link to it
    satisfies all of them while leaving the white-label surface un-white-labelled,
    which is exactly how #784 survived the first pass on this file."""
    for rewrite in hive_ui_surfaces.REWRITES:
        for token in ("Open WebUI", "openwebui.com", "open-webui", "tjbck", "Timothy"):
            assert token not in rewrite.replace, (rewrite.surface, token)


def test_the_about_tab_credit_is_removed_in_both_halves() -> None:
    """The credit line is a static anchor plus a runtime label written into the
    text node before it. Ship one without the other and Settings > About reads
    a bare "Created by" with nobody after it."""
    surfaces = {rewrite.surface for rewrite in hive_ui_surfaces.REWRITES}
    assert {"about-creator-link", "about-creator-label"} <= surfaces, surfaces
    label = next(r for r in hive_ui_surfaces.REWRITES if r.surface == "about-creator-label")
    link = next(r for r in hive_ui_surfaces.REWRITES if r.surface == "about-creator-link")
    assert "Created by" not in label.replace, label.replace
    assert "Timothy" not in link.replace and "tjbck" not in link.replace, link.replace
    # Node structure must survive: Svelte walks this template positionally, so
    # the anchor has to stay even though nothing renders inside it.
    assert link.replace.count("<a ") == link.find.count("<a "), link.replace
    assert link.replace.count("</div>") == link.find.count("</div>"), link.replace


# The #833 rewrites and the exact attributes each one is allowed to insert.
# The list is written out here rather than derived from the patch module on
# purpose: a test that read the answer off the thing it is checking would pass
# no matter what those rewrites did.
ACCESSIBILITY_ATTRIBUTES = {
    "sidebar-toggle-rail": (' aria-label="Open Sidebar"', ' aria-expanded="false"'),
    "sidebar-toggle-navbar": (' aria-label="Open Sidebar"', ' aria-expanded="false"'),
    "sidebar-toggle-expanded-state": (' aria-expanded="true"',),
    "navbar-temporary-chat-button": (' aria-label="Temporary Chat"',),
    "navbar-save-chat-button": (' aria-label="Save Chat"',),
    "navbar-chat-menu-button": (' aria-label="Chat Menu"',),
}


def test_the_accessibility_rewrites_only_add_attributes() -> None:
    """#833 is a naming fix, so none of its rewrites may change behaviour,
    styling or layout. Take the inserted attributes back out of `replace` and
    what is left has to be `find` byte for byte, which fails on any edit that
    also moved a node, altered a class or dropped a handler."""
    for surface, attributes in ACCESSIBILITY_ATTRIBUTES.items():
        rewrite = _rewrite(surface)
        stripped = rewrite.replace
        for attribute in attributes:
            assert stripped.count(attribute) == 1, (surface, attribute)
            stripped = stripped.replace(attribute, "", 1)
        assert stripped == rewrite.find, surface


def test_the_sidebar_toggle_gets_a_name_and_a_disclosure_state() -> None:
    """The reason #833 exists: a screen reader announced the control that
    opens the chat sidebar as a bare "button". A name on its own is still not
    enough for a disclosure, so each collapsed-state toggle also has to report
    that what it controls is closed, and the expanded counterpart has to
    report the opposite."""
    for surface in ("sidebar-toggle-rail", "sidebar-toggle-navbar"):
        rewrite = _rewrite(surface)
        assert "aria-label" not in rewrite.find, f"{surface} was already named"
        assert 'aria-label="Open Sidebar"' in rewrite.replace, surface
        assert 'aria-expanded="false"' in rewrite.replace, surface

    expanded = _rewrite("sidebar-toggle-expanded-state")
    assert "aria-expanded" not in expanded.find, expanded.find
    assert 'aria-expanded="true"' in expanded.replace, expanded.replace


def test_the_toggles_navbar_siblings_are_named_too() -> None:
    """An unnamed control rarely sits alone. The three beside the toggle kept
    their label in a tooltip, which is not an accessible name."""
    for surface in (
        "navbar-temporary-chat-button",
        "navbar-save-chat-button",
        "navbar-chat-menu-button",
    ):
        rewrite = _rewrite(surface)
        assert "aria-label" not in rewrite.find, f"{surface} was already named"
        assert "aria-label=" in rewrite.replace, surface


# --------------------------------------------------------------------------
# 1b. The rewrite table against the real, pinned bundle
#
# Everything above this line checks the table against itself: the excerpts in
# test_every_rewrite_removes_its_surface_from_a_real_bundle_excerpt are built
# out of `rewrite.find`, so a `find` edited into a string the shipped bundle no
# longer contains still passes. Only apply_ui_surfaces_patch.py catches that,
# and it runs where the image is built, which is owui-nightly.yml and
# deploy-demo-box.yml, never PR CI. So that edit used to reach `main` green and
# fail at deploy.
#
# pinned-bundle-excerpts.json closes it. It holds verbatim slices of
# /app/build/_app/immutable from the digest Dockerfile.open-webui pins, with
# 400 characters of surrounding context, extracted by dump_bundle_excerpts.py,
# which itself refuses to write unless every rewrite and guard matches exactly
# one site. Checking the table against those bytes needs no Docker and no
# network, so it runs in `make test-scripts` like everything else.
# --------------------------------------------------------------------------

def _excerpts() -> dict:
    return json.loads(EXCERPTS.read_text())


def test_the_excerpt_fixture_is_from_the_digest_the_dockerfile_pins() -> None:
    """The fixture is only evidence about the image actually being built. A
    digest bump that leaves it behind must fail here and force a regeneration,
    because a stale fixture would happily certify a table that no longer
    matches anything in the new bundle."""
    match = re.search(r"^FROM\s+(\S+@sha256:[0-9a-f]{64})\s*$", DOCKERFILE.read_text(), re.M)
    assert match, "Dockerfile.open-webui no longer pins its base image by digest"
    assert _excerpts()["image"] == match.group(1), (
        "pinned-bundle-excerpts.json was captured from a different image than "
        "Dockerfile.open-webui now pins. Regenerate it: "
        "python3 deploy/docker/owui-patches/dump_bundle_excerpts.py <extracted bundle>"
    )


def test_every_rewrite_matches_the_real_pinned_bundle() -> None:
    """A `find` that no longer occurs in the shipped bundle removes nothing and
    silently restores its surface. This is the check that sees that in review
    rather than at deploy."""
    excerpts = _excerpts()["rewrites"]
    surfaces = {rewrite.surface for rewrite in hive_ui_surfaces.REWRITES}
    assert set(excerpts) == surfaces, (set(excerpts) ^ surfaces)

    for rewrite in hive_ui_surfaces.REWRITES:
        sample = excerpts[rewrite.surface]["text"]
        assert sample.count(rewrite.find) == 1, (
            f"{rewrite.surface}: `find` does not occur exactly once in the real "
            f"bundle excerpt from {excerpts[rewrite.surface]['file']}"
        )
        patched, hits = hive_ui_surfaces.apply(sample)
        assert hits[rewrite.surface] == 1, (rewrite.surface, hits)
        assert rewrite.find not in patched, rewrite.surface
        assert rewrite.replace in patched, rewrite.surface


def test_the_rewrites_leave_the_kept_surfaces_alone_in_the_real_bundle() -> None:
    """The narrower version of this above uses the guard string alone. Here the
    guard arrives inside real neighbouring bundle bytes, which is where an
    over-broad rewrite would actually catch it."""
    excerpts = _excerpts()["guards"]
    assert set(excerpts) == {name for name, _ in hive_ui_surfaces.GUARDS}, set(excerpts)
    for name, needle in hive_ui_surfaces.GUARDS:
        sample = excerpts[name]["text"]
        assert sample.count(needle) == 1, name
        patched, _hits = hive_ui_surfaces.apply(sample)
        assert needle in patched, f"a rewrite swallowed the surface Hive keeps: {name}"


def test_the_build_patch_fails_when_a_rewrite_matches_nothing() -> None:
    """The build-time guard's own failure path, exercised here so it cannot
    quietly become a no-op. A rewrite that matched zero sites must be a hard
    failure: the pass otherwise reports success and the image ships with the
    surface back."""
    healthy = {rewrite.surface: rewrite.count for rewrite in hive_ui_surfaces.REWRITES}
    assert hive_ui_surfaces.verify_counts(healthy) == []

    for surface in healthy:
        starved = dict(healthy, **{surface: 0})
        failures = hive_ui_surfaces.verify_counts(starved)
        assert len(failures) == 1 and surface in failures[0], (surface, failures)

    # A surface missing from the totals entirely is the same failure, not a
    # KeyError and not a pass.
    absent = {k: v for k, v in healthy.items() if k != "workspace-models-tab"}
    assert any("workspace-models-tab" in f for f in hive_ui_surfaces.verify_counts(absent))


# --------------------------------------------------------------------------
# 2. Feature flags (compose value + persisted-config reconcile)
# --------------------------------------------------------------------------

def test_the_suggested_prompts_block_is_gone_from_the_source() -> None:
    """The chat landing surface rendered upstream's six sample starter prompts
    under a "Suggested" heading. The owner asked for the block itself gone, not
    hidden, so this asserts at the source rather than at a feature flag: the
    component is deleted, neither placeholder references it, and the sample
    content is out of the backend default. A vendor update that reintroduces any
    of the three puts someone else's product back on our landing page."""
    src = REPO / "vendor" / "open-webui" / "src"
    component = src / "lib" / "components" / "chat" / "Suggestions.svelte"
    assert not component.exists(), f"{component} must stay deleted"

    for placeholder in ("Placeholder.svelte", "ChatPlaceholder.svelte"):
        text = (src / "lib" / "components" / "chat" / placeholder).read_text(encoding="utf-8")
        assert "Suggestions" not in text, f"{placeholder} must not render suggestions"
        assert "suggestion_prompts" not in text, f"{placeholder} must not read suggestion prompts"

    config = (
        REPO / "vendor" / "open-webui" / "backend" / "open_webui" / "config.py"
    ).read_text(encoding="utf-8")
    for sample in ("Roman Empire", "options trading", "procrastinate"):
        assert sample not in config, f"upstream sample prompt {sample!r} is back in config.py"


def test_compose_turns_the_flag_backed_surfaces_off() -> None:
    compose = COMPOSE.read_text()
    for variable in FLAG_VARIABLES:
        assert f'{variable}: "false"' in compose, variable


def test_the_image_itself_ships_the_flag_backed_surfaces_off() -> None:
    """The compose values above only reach a deployment that repeats them.

    Every flag here is off on the demo box, and was off before this test
    existed, purely because docker-compose.yml names it. The image did not
    carry that position, so a plain `docker run` of the Hive image, which is
    exactly how this repo builds its own before/after proof screenshots,
    rendered Notes, Calendar, Automations and the Personalization tab in full.
    That is what the owner saw on 2026-08-17.

    A Docker `ENV` default, rather than a rewrite of upstream's `os.getenv`
    line, because it survives a digest bump with no literal to drift and
    compose still overrides it: an enterprise deployment that wants Notes back
    sets the variable to true and the reconcile below flips the persisted row.
    """
    declared = _image_env()
    for variable in FLAG_VARIABLES:
        assert declared.get(variable) == "false", (variable, declared)


def test_the_update_nag_is_off_in_the_image_and_not_in_the_reconcile() -> None:
    """Upstream's "A new version is now available" banner, which links a Hive
    customer to another vendor's release notes.

    docker-compose.yml has set `ENABLE_VERSION_UPDATE_CHECK` false since #143,
    so like the flags above it was off on the deployment and on everywhere
    else; it appeared in both of this change's own before and after captures
    until the image carried it too.

    It is deliberately NOT in FEATURE_CONFIG_ENV. Unlike the four above it is
    a plain environment read (`env.py`, surfaced straight into `/api/config`
    rather than through `config.get`), so there is no persisted row to
    reconcile and an entry there would write a key nothing reads, which is a
    no-op shaped like a control. That is the ENABLE_ADMIN_PANEL mistake the
    test above this one exists to prevent, so assert the absence too.
    """
    assert _image_env().get("ENABLE_VERSION_UPDATE_CHECK") == "false", _image_env()
    assert "ENABLE_VERSION_UPDATE_CHECK" not in hive_rag_env_config.FEATURE_CONFIG_ENV.values()
    assert "ENABLE_VERSION_UPDATE_CHECK" not in hive_rag_env_config.RAG_CONFIG_ENV.values()


def test_compose_no_longer_sets_variables_open_webui_does_not_read() -> None:
    """ENABLE_ADMIN_PANEL and ENABLE_MODEL_FILTER exist nowhere in the pinned
    v0.10.2 image, so setting either one is a no-op that reads like a control.
    Both are gone; assert they stay gone."""
    for line in COMPOSE.read_text().splitlines():
        stripped = line.strip()
        if stripped.startswith("#"):
            continue
        assert not stripped.startswith("ENABLE_ADMIN_PANEL:"), line
        assert not stripped.startswith("ENABLE_MODEL_FILTER:"), line


def test_flag_backed_surfaces_are_reconciled_onto_a_booted_database() -> None:
    """Open WebUI seeds every DEFAULT_CONFIG key on first boot and the row wins
    forever after, so without this neither the compose values nor the image
    defaults above reach the demo box, whose rows were seeded on in 2026."""
    for variable in FLAG_VARIABLES:
        assert variable in hive_rag_env_config.FEATURE_CONFIG_ENV.values(), variable

    keys = [key for key, var in hive_rag_env_config.FEATURE_CONFIG_ENV.items() if var in FLAG_VARIABLES]
    assert len(keys) == len(FLAG_VARIABLES), keys

    config = FakeConfig({key: True for key in keys})
    applied = asyncio.run(
        hive_rag_env_config.reconcile(config, {variable: "false" for variable in FLAG_VARIABLES})
    )
    for key in keys:
        # Booleans, not the string "false": a non-empty string is truthy both
        # in Python and in the frontend that reads it back out of /api/config,
        # so a string here would leave every one of these surfaces visible.
        assert config.stored[key] is False, (key, config.stored[key])
        assert applied[key] is False, (key, applied[key])


def test_reconcile_still_leaves_unset_feature_flags_alone() -> None:
    config = FakeConfig({"notes.enable": True})
    applied = asyncio.run(hive_rag_env_config.reconcile(config, {"ENABLE_NOTES": "  "}))
    assert applied == {}, applied
    assert config.stored["notes.enable"] is True


def test_reconcile_passes_a_true_flag_through_as_a_boolean() -> None:
    """An enterprise deployment that wants Notes back sets the variable to
    true, and must get a real boolean out rather than a truthy string."""
    config = FakeConfig({"notes.enable": False})
    applied = asyncio.run(hive_rag_env_config.reconcile(config, {"ENABLE_NOTES": "True"}))
    assert applied["notes.enable"] is True, applied


# --------------------------------------------------------------------------
# 3. Caddy path block
# --------------------------------------------------------------------------

def _removed_surface_pattern() -> re.Pattern:
    match = re.search(
        r"^\s*path_regexp\s+removed\s+(\S+)\s*$", CADDYFILE.read_text(), re.MULTILINE
    )
    assert match, "Caddyfile.owui no longer defines the `removed` path_regexp"
    return re.compile(match.group(1))


def _blocked_pattern() -> re.Pattern:
    match = re.search(
        r"^\s*path_regexp\s+blocked\s+(\S+)\s*$", CADDYFILE.read_text(), re.MULTILINE
    )
    assert match, "Caddyfile.owui no longer defines the `blocked` path_regexp"
    return re.compile(match.group(1))


def test_the_notes_api_is_blocked_because_its_router_enforces_no_flag() -> None:
    """The one removed feature whose backend does not refuse on its own flag.

    Turning a feature flag off is an honest removal only when the flag reaches
    the API too. In the pinned v0.10.2 image `calendar.py` checks
    `calendar.enable` on all 13 of its routes, `automations.py` checks
    `automations.enable` on all 8, and `memories.py` checks `memories.enable`
    on all 11, so for those three the flag is the whole control and a second
    one here would be decoration. `notes.py` checks `notes.enable` on none of
    its 9 routes: it imports the config module only for the admin export and
    access-control constants. With the navigation entry gone and the page route
    already 404'd, every one of those 9 endpoints stayed callable by any signed
    in user, which is precisely a hidden entry pointing at live code.
    """
    pattern = _blocked_pattern()
    for path in (
        "/api/v1/notes",
        "/api/v1/notes/",
        "/api/v1/notes/01234567-89ab-cdef-0123-456789abcdef",
        "/api/v1/notes/01234567-89ab-cdef-0123-456789abcdef/update",
        "//api/v1/notes",
        "/API/V1/Notes",
    ):
        assert pattern.match(path), path


def test_the_notes_block_spares_every_api_hive_keeps() -> None:
    pattern = _blocked_pattern()
    for path in (
        "/api/config",
        "/api/v1/chats/",
        "/api/v1/knowledge/list",
        "/api/v1/files/",
        "/api/v1/retrieval/query/collection",
        "/api/v1/auths/",
        "/openai/models",
        # Not a prefix match: only the whole segment counts.
        "/api/v1/notesomething",
        "/notes",
    ):
        assert not pattern.match(path), path


def test_removed_routes_are_unreachable_by_url() -> None:
    pattern = _removed_surface_pattern()
    must_block = [
        "/playground",
        "/playground/",
        "//playground",
        "/PlayGround",
        "/notes",
        "/notes/01234567-89ab-cdef-0123-456789abcdef",
        "/calendar",
        "/automations",
        "/automations/new",
        "/workspace/models",
        "/workspace/models/create",
        "/workspace/prompts",
        "/workspace/skills",
        "/workspace/skills/create",
        "/workspace/tools",
    ]
    for path in must_block:
        assert pattern.match(path), path


def test_the_block_spares_the_surfaces_hive_keeps() -> None:
    pattern = _removed_surface_pattern()
    must_pass = [
        "/",
        "/workspace",
        "/workspace/",
        "/workspace/knowledge",
        "/workspace/knowledge/abc",
        # Open WebUI's own APIs, including the ones the sidebar and the chat
        # request path call on every load. This regex is scoped to page routes
        # and must not reach any of them; the separate `blocked` regex is where
        # /api/v1/notes is refused, asserted above.
        "/api/config",
        "/api/v1/chats/",
        "/api/v1/knowledge/list",
        "/api/v1/models",
        "/_app/immutable/nodes/7.js",
        # Not a route prefix match: only the whole segment counts.
        "/notesomething",
        "/calendarium",
    ]
    for path in must_pass:
        assert not pattern.match(path), path


# --------------------------------------------------------------------------
# 4. Coverage: no surface on the issue's list is left uncovered
# --------------------------------------------------------------------------

def test_every_hidden_surface_has_a_live_mechanism() -> None:
    """The test that fails when a hidden surface comes back. Each name is
    matched against whichever artifact is supposed to be removing it, so
    deleting a rewrite entry, a compose flag or a Caddy alternative all
    surface here rather than in a screenshot months later."""
    rewrites = " ".join(rewrite.surface for rewrite in hive_ui_surfaces.REWRITES)
    compose = COMPOSE.read_text()
    caddy_pattern = _removed_surface_pattern().pattern

    aliases = {
        "workspace-models": ("workspace-models-tab", "ENABLE_", "models"),
        "workspace-prompts": ("workspace-prompts-tab", "ENABLE_", "prompts"),
        "workspace-skills": ("workspace-skills-tab", "ENABLE_", "skills"),
        "workspace-tools": ("workspace-tools-tab", "ENABLE_", "tools"),
        "playground": ("playground-item", "ENABLE_", "playground"),
        "notes": ("", "ENABLE_NOTES", "notes"),
        "calendar": ("", "ENABLE_CALENDAR", "calendar"),
        "automations": ("", "ENABLE_AUTOMATIONS", "automations"),
        # Settings > Personalization is a dialog tab, not a route, so there is
        # no page path for Caddy to block. Its API is refused by its own
        # router on the same flag, asserted separately above.
        "memories": ("", "ENABLE_MEMORIES", ""),
        "vendor-links": ("usermenu-vendor-links", "ENABLE_", ""),
        # Both live inside the Settings dialog, which is not a route, so there
        # is no page path for Caddy to block. /admin/settings, where the first
        # one pointed, is already covered by the separate @blocked admin regex.
        "settings-admin-link": ("settings-admin-link", "ENABLE_", ""),
        "settings-vendor-translate-link": ("settings-vendor-translate-link", "ENABLE_", ""),
        "changelog-modal": ("changelog-modal", "ENABLE_", ""),
        # Settings > About is a dialog, not a route, so there is no path for
        # Caddy to block; the bundle rewrite is the whole mechanism.
        "about-vendor-copyright": ("about-vendor-copyright", "ENABLE_", ""),
        "about-creator-credit": ("about-creator-link", "ENABLE_", ""),
        "about-vendor-social": ("about-vendor-social-badges", "ENABLE_", ""),
    }

    for surface, mechanism in sorted(HIDDEN_SURFACES.items()):
        rewrite_token, flag_token, caddy_token = aliases[surface]
        if mechanism == "bundle":
            assert rewrite_token in rewrites, surface
        else:
            assert f'{flag_token}: "false"' in compose, surface
        if caddy_token:
            assert caddy_token in caddy_pattern, surface


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui removed chat surfaces (issue #772)")


if __name__ == "__main__":
    sys.exit(main())
