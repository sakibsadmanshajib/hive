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
    REPO_ROOT
    / "vendor"
    / "open-webui"
    / "backend"
    / "open_webui"
    / "utils"
    / "hive_display_name.py"
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


def test_deliberate_casing_survives() -> None:
    """Capitalizing every word would turn McDonald into Mcdonald, and a name the
    person already spelled correctly is not ours to rewrite."""
    assert derive("McDonald@example.com") == "McDonald"
    assert derive("first.McDonald@example.com") == "First McDonald"


def test_digits_are_left_alone() -> None:
    assert derive("user123@example.com") == "User123"
    assert derive("42@example.com") == "42"


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
    """A helper nothing calls is not a fix. Asserted against the file because
    the Open WebUI backend cannot be imported here without its dependencies."""
    oauth = (
        REPO_ROOT
        / "vendor"
        / "open-webui"
        / "backend"
        / "open_webui"
        / "utils"
        / "oauth.py"
    ).read_text(encoding="utf-8")
    assert (
        "from open_webui.utils.hive_display_name import display_name_from_email" in oauth
    ), "oauth.py must import the derivation"
    assert (
        "name = display_name_from_email(email)" in oauth
    ), "oauth.py must derive the name when the username claim is missing"
    assert (
        "name = email" not in oauth
    ), "oauth.py must not fall back to storing the raw email address as a name"


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui display name derivation")


if __name__ == "__main__":
    sys.exit(main())
