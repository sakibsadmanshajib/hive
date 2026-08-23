#!/usr/bin/env python3
"""Self-check for the Open WebUI OAuth callback landing patch.

The callback's success redirect used to hardcode `/auth`, whose only job was
converting the token cookie into a stored session before client-side
navigating to `/` anyway; one extra full HTML document per sign-in for
nothing. apply_oauth_callback_landing_patch.py retargets that leg to `/`,
leaving the error leg (?error= back to the sign-in page) untouched.

This file runs the real patch script against a throwaway copy of the real
vendored oauth.py, so a future open-webui digest bump that shifts the callback
fails here in CI instead of failing an image build on a demo box. It also pins
the pairing invariant: the frontend half (the token-cookie recovery block in
the root layout) must exist for the backend retarget to be safe.
No framework, no network, no Open WebUI import.
Run: python3 scripts/test_owui_oauth_callback_landing.py
"""
import ast
import pathlib
import shutil
import tempfile

REPO = pathlib.Path(__file__).resolve().parents[1]
PATCH = REPO / "deploy" / "docker" / "owui-patches" / "apply_oauth_callback_landing_patch.py"
VENDOR_OAUTH = REPO / "vendor" / "open-webui" / "backend" / "open_webui" / "utils" / "oauth.py"
ROOT_LAYOUT = REPO / "vendor" / "open-webui" / "src" / "routes" / "+layout.svelte"

ANCHOR = "        redirect_url = f'{redirect_base_url}/auth'\n"
REPLACEMENT = "        redirect_url = f'{redirect_base_url}/'\n"
ERROR_LEG = "redirect_url}?error="
RECOVERY_MARK = "rawCookie"


def run_patch_against(target_path: pathlib.Path) -> None:
    """Execute the build-time patch script with its TARGET pointed at a
    temporary copy instead of the image's /app path."""
    source = PATCH.read_text()
    image_path = "/app/backend/open_webui/utils/oauth.py"
    assert image_path in source, "patch script no longer names its image target"
    source = source.replace(image_path, str(target_path))
    exec(compile(source, str(PATCH), "exec"), {"__name__": "__main__"})  # noqa: S102


def test_patch_retargets_success_leg_only():
    with tempfile.TemporaryDirectory() as tmp:
        target = pathlib.Path(tmp) / "oauth.py"
        shutil.copy(VENDOR_OAUTH, target)

        run_patch_against(target)

        patched = target.read_text()
        assert ANCHOR not in patched, "stale '/auth' success target survived"
        assert patched.count(REPLACEMENT) == 1, "success leg was not retargeted to '/'"
        assert ERROR_LEG in patched, "error leg must keep landing on the sign-in page"
        ast.parse(patched), "patched oauth.py does not parse"


def test_second_run_fails_loudly():
    """Idempotence posture: re-application is a silent no-op risk, so it must
    fail the assertion rather than pass quietly."""
    with tempfile.TemporaryDirectory() as tmp:
        target = pathlib.Path(tmp) / "oauth.py"
        shutil.copy(VENDOR_OAUTH, target)
        run_patch_against(target)
        try:
            run_patch_against(target)
        except AssertionError:
            return
        raise AssertionError("running the patch twice did not fail loudly")


def test_shifted_upstream_fails_the_asserts():
    """A vendor bump that removes or renames the redirect line must trip the
    patch's own assertion, never silently skip."""
    with tempfile.TemporaryDirectory() as tmp:
        target = pathlib.Path(tmp) / "oauth.py"
        shutil.copy(VENDOR_OAUTH, target)
        drifted = target.read_text().replace(ANCHOR, "")
        assert drifted != target.read_text()
        target.write_text(drifted)
        try:
            run_patch_against(target)
        except AssertionError:
            return
        raise AssertionError("drifted upstream passed the patch without its anchor")


def test_frontend_half_is_present():
    """The backend retarget is only safe because the root layout converts the
    token cookie into a session itself. If that block disappears (an upstream
    layout rewrite, a bad merge) this check fails before the pair ships
    half-applied."""
    layout = ROOT_LAYOUT.read_text()
    assert RECOVERY_MARK in layout, (
        "root layout no longer recovers the OAuth token cookie; the callback "
        "landing patch would strand users on an anonymous page"
    )
    assert "ssoAutoRedirectDecision" in layout, (
        "root layout no longer consults the tested SSO decision function"
    )


def main() -> int:
    test_patch_retargets_success_leg_only()
    test_second_run_fails_loudly()
    test_shifted_upstream_fails_the_asserts()
    test_frontend_half_is_present()
    print("ok: oauth callback landing patch retargets '/', keeps ?error= leg, "
          "fails loudly on drift and reapplication")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
