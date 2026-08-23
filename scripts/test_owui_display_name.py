#!/usr/bin/env python3
"""Self-check for the Open WebUI display-name derivation.

Open WebUI provisions an OAuth user with the configured username claim and, when
that claim is missing, stored the email address in the `name` column. Supabase's
OAuth authorization server sends no name claim and no user metadata, so five of
six accounts on the demo box were greeted by their own email address.

vendor/open-webui/backend/open_webui/utils/hive_display_name.py derives a name
from the local part of the address instead. This file exercises it directly: no
framework, no network, no Open WebUI import (the module deliberately depends on
nothing but the standard library so this stays true).
Run: python3 scripts/test_owui_display_name.py
"""

import importlib.util
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
MODULE_PATH = (
    REPO_ROOT / "deploy" / "docker" / "owui-patches" / "hive_display_name.py"
)
spec = importlib.util.spec_from_file_location("hive_display_name", MODULE_PATH)
hive_display_name = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hive_display_name)

derive = hive_display_name.display_name_from_email


def test_a_dotted_local_part_becomes_a_name() -> None:
    """The common shape, and the whole point of the change."""
    assert derive("first.last@example.com") == "First Last"


def test_other_separators_are_treated_as_spaces() -> None:
    assert derive("first_last@example.com") == "First Last"
    assert derive("first-last@example.com") == "First Last"
    assert derive("first..last@example.com") == "First Last"


def test_a_single_word_local_part_is_just_capitalized() -> None:
    assert derive("demo@example.invalid") == "Demo"


def test_plus_addressing_is_a_routing_tag_not_a_name() -> None:
    assert derive("first.last+chat@example.com") == "First Last"


def test_casing_is_normalized_because_the_caller_lower_cases_first() -> None:
    """`handle_callback` lower cases the address before provisioning, so there is
    never mixed case to preserve by the time this runs. Asserting the real
    behaviour rather than a nicer sounding promise the code does not keep."""
    assert derive("mcdonald@example.com") == "Mcdonald"
    assert derive("first.mcdonald@example.com") == "First Mcdonald"


def test_a_hostile_address_cannot_smuggle_control_or_bidi_characters() -> None:
    """The value is rendered next to other people's names, so a right to left
    override that makes one string display as another does not belong in it."""
    derived = derive("e\u202evil@example.com")
    assert "\u202e" not in derived, derived
    assert derived == "Evil"


def test_a_local_part_that_sanitizes_away_still_cannot_smuggle_an_override() -> None:
    """The fallback path, which the test above does not reach. A local part made
    only of characters _sanitize drops leaves no words to join, and returning the
    address unchanged there put the override straight into the stored name."""
    derived = derive("\u202e@example.com")
    assert "\u202e" not in derived, derived
    assert derived == "@example.com", derived

    # And when nothing legible survives, a neutral literal rather than a name
    # made of punctuation. `_sanitize` leaves the "@" of any address that has
    # one, so the guard is "no alphanumeric left" rather than "empty".
    assert derive("\u202e@\u202e") == "User"
    assert derive("\u202e") == "User"


def test_a_very_long_local_part_is_capped() -> None:
    """A wall of text is not a name, and it reaches every surface that renders
    one."""
    derived = derive("a" * 500 + "@example.com")
    assert len(derived) <= 64, len(derived)


def test_digits_are_left_alone() -> None:
    assert derive("user123@example.com") == "User123"
    assert derive("42@example.com") == "42"


def test_incidental_whitespace_does_not_reach_the_display_name() -> None:
    """A stray space around the claim value would otherwise be stored and then
    rendered in the greeting."""
    assert derive("  first.last@example.com  ") == "First Last"


def test_a_quoted_local_part_does_not_produce_stray_quotes() -> None:
    """Legal but rare. The quotes are syntax, not part of anyone's name."""
    assert derive('"first.last"@example.com') == "First Last"


def test_an_underivable_address_falls_back_to_the_address() -> None:
    """A cosmetic problem must never become a failed sign in, so the function
    always returns something for a non-empty address."""
    assert derive("...@example.com") == "...@example.com"
    assert derive("") == ""


def test_the_result_is_never_the_raw_email_for_a_normal_address() -> None:
    """The regression this exists to prevent: five of six live accounts stored
    their own email address as their display name."""
    for address in (
        "first.last@example.com",
        "demo@example.invalid",
        "someone@example.co.uk",
    ):
        assert derive(address) != address, address
        assert "@" not in derive(address), address


def test_the_provisioning_path_actually_calls_it() -> None:
    """A helper nothing calls is not a fix, and the obvious place to call it is
    the wrong one: this image builds only the front end from vendor/open-webui
    and takes its backend from the pinned upstream image, so an edit to the
    vendored backend never runs. The call is spliced into the real backend at
    build time, like every other Hive backend change here."""
    patch = (
        REPO_ROOT
        / "deploy"
        / "docker"
        / "owui-patches"
        / "apply_display_name_patch.py"
    ).read_text(encoding="utf-8")
    assert (
        "from open_webui.utils.hive_display_name import display_name_from_email" in patch
    ), "the patch must import the derivation into oauth.py"
    assert (
        "name = display_name_from_email(email)" in patch
    ), "the patch must derive the name when the username claim is missing"
    assert (
        "name = email\n" in patch
    ), "the patch must still target upstream's email-as-name fallback"
    assert (
        "if new_name and new_name != user.name:" in patch
    ), "the patch must assert the refresh path still skips an absent claim"


def test_the_image_build_applies_the_patch() -> None:
    """The patch file existing proves nothing; the Dockerfile has to run it, and
    has to fail the build if the rewrite stopped matching."""
    dockerfile = (
        REPO_ROOT / "deploy" / "docker" / "Dockerfile.open-webui"
    ).read_text(encoding="utf-8")
    assert "apply_display_name_patch.py" in dockerfile, "the build must run the patch"
    assert (
        "owui-patches/hive_display_name.py /app/backend/open_webui/utils/hive_display_name.py"
        in dockerfile
    ), "the build must place the module the patched import resolves to"
    assert (
        "grep -q 'display_name_from_email(email)'" in dockerfile
    ), "the build must assert the rewrite landed"


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui display name derivation")


if __name__ == "__main__":
    sys.exit(main())
