#!/usr/bin/env python3
"""Self-checks for scripts/check-oauth-scopes.py.

No framework, no network, matching the other scripts/test_*.py in this repo.

The point of this file is narrow and specific: prove the comparator can go RED.
A subset check that only ever runs against a corpus it agrees with is
indistinguishable from a check that returns 0 unconditionally, and this
repository has shipped that shape before. So every assertion below that matters
is a negative one, and the headline case is #787 itself, replayed: the exact
scope string that killed sign-in, against the exact scopes_supported the
self-hosted GoTrue answers with, must exit 1.
"""
from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("check_oauth_scopes", HERE / "check-oauth-scopes.py")
assert SPEC and SPEC.loader
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)

# What the deployed self-hosted GoTrue actually answers with. Copied from
# https://console-hive.scubed.co/auth/v1/.well-known/openid-configuration on
# 2026-08-22, and identical to SupportedOAuthScopes in
# internal/models/oauth_scope.go at the pinned supabase/gotrue v2.189.0.
SELF_HOSTED = ["openid", "profile", "email", "phone"]

# What hosted Supabase advertised, which is the document #787 was validated
# against. Kept so the fixture corpus contains a server where offline_access is
# legitimate: a check that says no to everything is as useless as one that says
# yes to everything.
HOSTED = ["openid", "profile", "email", "phone", "offline_access"]

failures: list[str] = []


def expect(condition: bool, what: str) -> None:
    if condition:
        print(f"ok   {what}")
    else:
        failures.append(what)
        print(f"FAIL {what}")


def corpus(tmp: Path, *scope_values: str) -> Path:
    """A throwaway deploy/ tree declaring the given OAUTH_SCOPES values."""
    deploy = tmp / "deploy" / "docker"
    deploy.mkdir(parents=True, exist_ok=True)
    for index, value in enumerate(scope_values):
        (deploy / f"docker-compose.{index}.yml").write_text(
            "services:\n"
            "  open-webui:\n"
            "    environment:\n"
            "      # OAUTH_SCOPES: \"openid commented_out\"\n"
            f'      OAUTH_SCOPES: "{value}"\n'
        )
    return tmp / "deploy"


def list_corpus(tmp: Path, value: str) -> Path:
    """The same declaration in Compose's OTHER environment spelling.

    Compose accepts a list of `NAME=value` strings as readily as a mapping. A
    checker that reads only the mapping form reports a clean run over a corpus
    of zero when someone writes the list form, which is the original defect
    wearing a different hat.
    """
    deploy = tmp / "deploy" / "docker"
    deploy.mkdir(parents=True, exist_ok=True)
    (deploy / "docker-compose.list.yml").write_text(
        "services:\n"
        "  open-webui:\n"
        "    environment:\n"
        f"      - OAUTH_SCOPES={value}\n"
    )
    return tmp / "deploy"


def verdict(deploy: Path, supported: list[str]) -> tuple[int, str]:
    """Exit code plus everything the check printed.

    Output is captured rather than let through, for two reasons. The failure
    messages are GitHub `::error` annotations, and a passing test that emits a
    dozen of them into a CI log trains people to ignore real ones. And having
    the text in hand lets a case assert WHICH scope was named, not merely that
    something failed.
    """
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        code = check.report(check.declarations(deploy), supported, "fixture", deploy)
    return code, out.getvalue() + err.getvalue()


def main() -> int:
    with tempfile.TemporaryDirectory() as raw:
        tmp = Path(raw)

        # The regression itself. #787's exact value against the deployed
        # server's exact capability list.
        red = corpus(tmp / "787", "openid email profile offline_access")
        code, output = verdict(red, SELF_HOSTED)
        expect(code == 1, "#787's scope string fails against self-hosted GoTrue")
        expect("offline_access" in output, "the failure names the offending scope")
        expect("#787" in output, "the failure cites the incident")

        # The fix. Same corpus shape, offline_access removed.
        green = corpus(tmp / "fixed", "openid email profile")
        expect(verdict(green, SELF_HOSTED)[0] == 0, "the fixed scope string passes")

        # Not a check that refuses everything: offline_access is fine against a
        # server that advertises it.
        expect(verdict(red, HOSTED)[0] == 0, "#787's scope string passes against hosted Supabase")

        # A second file cannot smuggle the bad value back in past a first file
        # that is clean, which is the whole reason the corpus is every deploy
        # YAML rather than one known line.
        both = corpus(tmp / "both", "openid email profile", "openid offline_access")
        expect(verdict(both, SELF_HOSTED)[0] == 1, "a bad value in a second deploy file fails")

        # Compose's list spelling is checked too, or it is a hole the size of
        # the whole defect: not merely a missed failure but a silent pass over
        # zero declarations.
        list_red = list_corpus(tmp / "list-bad", "openid email profile offline_access")
        expect(verdict(list_red, SELF_HOSTED)[0] == 1,
               "list-form `- OAUTH_SCOPES=...` with a bad scope fails")
        list_green = list_corpus(tmp / "list-ok", "openid email profile")
        expect(verdict(list_green, SELF_HOSTED)[0] == 0, "list-form with a good scope passes")
        expect(len(check.declarations(list_green)) == 1, "list form is seen as one declaration")

        # A commented-out declaration is not a declaration. Every fixture file
        # above carries one, so this asserts the scanner ignored it rather than
        # counting it, and the fixed corpus passing at all is that proof.
        expect(
            len(check.declarations(corpus(tmp / "comment", "openid email profile"))) == 1,
            "a commented OAUTH_SCOPES line is not counted as a declaration",
        )

        # An empty corpus fails rather than passing over nothing. This is the
        # shape that turns a moved config into a permanently green check.
        empty = tmp / "empty" / "deploy"
        empty.mkdir(parents=True)
        expect(verdict(empty, SELF_HOSTED)[0] == 1, "an empty deploy corpus fails")

        # A discovery document that advertises no scopes is an error, not a
        # server that permits everything.
        for broken, why in (
            ({}, "no scopes_supported key"),
            ({"scopes_supported": []}, "an empty scopes_supported"),
            ({"scopes_supported": "openid"}, "a non-list scopes_supported"),
            ("not a document", "a non-object document"),
        ):
            try:
                check.read_supported(broken, "fixture")
                expect(False, f"{why} is rejected")
            except RuntimeError:
                expect(True, f"{why} is rejected")

        # An unreachable server is exit 1, never a quiet pass.
        try:
            check.fetch_supported("http://127.0.0.1:1/nothing-here")
            expect(False, "an unreachable discovery URL raises")
        except RuntimeError:
            expect(True, "an unreachable discovery URL raises")

        # A non-http scheme is refused rather than fetched. Without this, a
        # local file could decide whether the gate passes, and the URL comes
        # out of a deployment's own configuration.
        local = tmp / "local-discovery.json"
        local.write_text(json.dumps({"scopes_supported": ["openid", "offline_access"]}))
        for hostile in (f"file://{local}", "ftp://example.invalid/d.json", "gopher://x/"):
            try:
                check.fetch_supported(hostile)
                expect(False, f"{hostile.split(':')[0]}:// is refused")
            except RuntimeError:
                expect(True, f"{hostile.split(':')[0]}:// is refused")

        # End to end through main(), against a discovery document on disk, so
        # the argument plumbing is exercised and not just the internals.
        document = tmp / "discovery.json"
        document.write_text(json.dumps({"scopes_supported": SELF_HOSTED}))
        saved_deploy, saved_root = check.DEPLOY_DIR, check.REPO_ROOT
        try:
            check.DEPLOY_DIR, check.REPO_ROOT = red, tmp / "787"
            sys.argv = ["check-oauth-scopes.py", "--discovery-file", str(document)]
            out, err = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                bad = check.main()
                check.DEPLOY_DIR, check.REPO_ROOT = green, tmp / "fixed"
                good = check.main()
            expect(bad == 1, "main() exits 1 on the #787 corpus")
            expect(good == 0, "main() exits 0 on the fixed corpus")
        finally:
            check.DEPLOY_DIR, check.REPO_ROOT = saved_deploy, saved_root

    # The real repository must pass against the real deployed capability list.
    # This is what makes the check part of the build rather than a library: if
    # someone adds an unadvertised scope to deploy YAML, this fails offline,
    # without waiting for the live gate to reach the box.
    expect(
        verdict(check.DEPLOY_DIR, SELF_HOSTED)[0] == 0,
        "this repository's own deploy YAML passes against self-hosted GoTrue",
    )

    if failures:
        print(f"\n{len(failures)} check(s) failed:")
        for item in failures:
            print(f"  - {item}")
        return 1
    print("\nall checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
