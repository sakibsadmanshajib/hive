#!/usr/bin/env python3
"""Guard the chat model picker filter against the way its predecessor failed
(issues #772, #776, #792).

#776 hid `hive-embedding-default`, `hive-stt` and `hive-tts` by writing Open
WebUI's per-model `access_control`. It was reviewed, tested, merged and
deployed, and it changed nothing, because `deploy/docker/docker-compose.yml`
sets `BYPASS_MODEL_ACCESS_CONTROL: "true"` and the pinned v0.10.2 image then
skips `get_filtered_models` for every role. Every test that shipped with it
still passed, because they all asserted that the right values were WRITTEN and
none asserted that anything was READ.

So this file does not check that a value was written. It checks that the
filter is on the response path and that nothing can switch it off:

  1. The filter itself, executed, against a realistic six-alias listing.
  2. The real patch, applied to a verbatim excerpt of the pinned image's own
     `/api/models` handler, asserting the call lands after upstream's own
     filtering, before the return, and at the handler's own indentation, so no
     flag, role or access-control branch gates it. PR CI never builds
     Dockerfile.open-webui, which is why the excerpt is checked in.
  3. The wiring: the Dockerfile must both stage and run the patch, and compose
     must name the aliases. A patch that ships without its RUN line is
     precisely as inert as #776 was.
  4. The coupling that caused #792: while compose sets the bypass, the repo
     must carry a picker filter that does not depend on access control.

`--live BASE --token TOKEN` runs the assertion that a unit test cannot make:
against a booted Open WebUI, the gateway's own list must still carry all three
aliases while the picker's list carries none of them and is not empty. That is
the shape of the bug, end to end, and it is the check that goes red the moment
a fix is inert again.

No framework, no network, no Docker in the default mode.
Run: python3 scripts/test_owui_model_picker_filter.py
"""

import argparse
import importlib.util
import json
import sys
import urllib.error
import urllib.request
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
PATCHES = REPO / "deploy" / "docker" / "owui-patches"
COMPOSE = REPO / "deploy" / "docker" / "docker-compose.yml"
DOCKERFILE = REPO / "deploy" / "docker" / "Dockerfile.open-webui"
EXCERPTS = PATCHES / "pinned-main-excerpts.json"

# The aliases #772 reported and #792 re-reported. Every one of them is a real
# row in the catalog and must stay on the gateway's own /v1/models.
NON_CHAT_ALIASES = ("hive-embedding-default", "hive-stt", "hive-tts")
CHAT_ALIASES = ("hive-auto", "hive-default", "hive-fast")


def _load(name: str):
    spec = importlib.util.spec_from_file_location(name, PATCHES / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


hive_model_picker = _load("hive_model_picker")
apply_model_picker_patch = _load("apply_model_picker_patch")

# Reuse the Dockerfile instruction parser rather than writing a second one. It
# already folds backslash continuations, which matters: a substring check for
# the filename is satisfied by the COPY line alone and stays green with the RUN
# line neutered, which is the one arrangement that ships the aliases back.
spec = importlib.util.spec_from_file_location(
    "test_caddy_owui_blocklist", REPO / "scripts" / "test_caddy_owui_blocklist.py"
)
_blocklist = importlib.util.module_from_spec(spec)
spec.loader.exec_module(_blocklist)
dockerfile_runs_patch = _blocklist._dockerfile_runs_patch


def listing(ids):
    """A /api/models-shaped listing, as Open WebUI builds it."""
    return [{"id": alias, "name": alias, "owned_by": "openai"} for alias in ids]


# --------------------------------------------------------------------------
# 1. The filter, executed
# --------------------------------------------------------------------------

def test_filter_drops_only_the_non_chat_aliases() -> None:
    env = {"HIVE_PICKER_HIDDEN_MODEL_IDS": ",".join(NON_CHAT_ALIASES)}
    kept = hive_model_picker.filter_models(listing(CHAT_ALIASES + NON_CHAT_ALIASES), env)
    assert [m["id"] for m in kept] == list(CHAT_ALIASES), kept


def test_filter_is_a_no_op_when_nothing_is_configured() -> None:
    """An unset variable must never empty the picker.

    An empty dropdown is the worse failure of the two: hiding three aliases
    annoys, hiding all six looks identical to a gateway 403 and hid one for
    four nights (#717).
    """
    every = listing(CHAT_ALIASES + NON_CHAT_ALIASES)
    assert hive_model_picker.filter_models(every, {}) == every
    assert hive_model_picker.filter_models(every, {"HIVE_PICKER_HIDDEN_MODEL_IDS": "  "}) == every


def test_filter_reads_open_webuis_own_modality_settings() -> None:
    """The admin-selected embedding alias must not need a second mention.

    RAG_EMBEDDING_MODEL is admin-selectable and drives vector-store
    provisioning (D-001), so a deployment that changes it must not silently
    start listing the new alias in the chat picker.
    """
    env = {
        "RAG_EMBEDDING_MODEL": "hive-embedding-bge-m3",
        "AUDIO_TTS_MODEL": "hive-tts",
        "AUDIO_STT_MODEL": "hive-stt",
    }
    ids = CHAT_ALIASES + ("hive-embedding-bge-m3", "hive-stt", "hive-tts")
    kept = hive_model_picker.filter_models(listing(ids), env)
    assert [m["id"] for m in kept] == list(CHAT_ALIASES), kept


def test_filter_ignores_entries_without_an_id() -> None:
    env = {"HIVE_PICKER_HIDDEN_MODEL_IDS": "hive-tts"}
    kept = hive_model_picker.filter_models([{"name": "no id"}, {"id": "hive-tts"}], env)
    assert kept == [{"name": "no id"}]


# --------------------------------------------------------------------------
# 2. The patch, applied to the pinned image's own handler
# --------------------------------------------------------------------------

def test_patch_applies_to_the_pinned_api_models_handler() -> None:
    fixture = json.loads(EXCERPTS.read_text())
    patched = apply_model_picker_patch.patch(fixture["api_models_handler"])
    body = apply_model_picker_patch.handler_body(patched)

    assert "hive_model_picker" in body, "the filter is not in the handler at all"
    # patch() asserts ordering and non-gating itself; re-assert here so this
    # file fails, and names why, if those assertions are ever weakened.
    apply_model_picker_patch.assert_unconditional(body)
    assert body.index(apply_model_picker_patch.CALL) < body.index(
        apply_model_picker_patch.RETURN
    ), "the filter runs after the handler returns, so the response is unfiltered"


def test_patch_refuses_a_handler_it_cannot_verify() -> None:
    """A digest bump that moves the handler must break the build, not pass.

    sed exits 0 on a zero-match address and str.replace is silent on a miss;
    this is what makes the difference between a patch and a no-op visible.
    """
    fixture = json.loads(EXCERPTS.read_text())
    drifted = fixture["api_models_handler"].replace(
        "models = await get_filtered_models(models, user)", "models = models"
    )
    try:
        apply_model_picker_patch.patch(drifted)
    except AssertionError:
        return
    raise AssertionError("patch() accepted a handler whose anchor is gone")


def test_patch_rejects_a_gated_filter() -> None:
    """The #776 failure mode, expressed as a test.

    A filter placed inside any conditional is a filter a deployment flag can
    switch off. Open WebUI offers no conditional here that this deployment does
    not already disable, so the only correct placement is unconditional.
    """
    gated = (
        "    if not BYPASS_MODEL_ACCESS_CONTROL:\n"
        "        models = _hive_filter_models(models, os.environ)\n"
    )
    try:
        apply_model_picker_patch.assert_unconditional(gated)
    except AssertionError:
        return
    raise AssertionError(
        "assert_unconditional accepted a filter behind BYPASS_MODEL_ACCESS_CONTROL, "
        "which is exactly the arrangement that made #776 inert"
    )


# --------------------------------------------------------------------------
# 3. Wiring: the patch has to actually reach the image, and compose has to
#    name the aliases
# --------------------------------------------------------------------------

def test_dockerfile_stages_and_runs_the_patch() -> None:
    staged, invoked = dockerfile_runs_patch(DOCKERFILE, "apply_model_picker_patch.py")
    assert staged, f"{DOCKERFILE.name} no longer COPYs apply_model_picker_patch.py"
    assert invoked, (
        f"{DOCKERFILE.name} no longer RUNs apply_model_picker_patch.py, so the "
        "three non-chat aliases are back in the picker"
    )
    text = DOCKERFILE.read_text(encoding="utf-8")
    assert "owui-patches/hive_model_picker.py" in text, (
        f"{DOCKERFILE.name} no longer COPYs hive_model_picker.py into the image, "
        "so the spliced import raises at startup"
    )


def test_compose_names_the_non_chat_aliases() -> None:
    text = COMPOSE.read_text(encoding="utf-8")
    assignments = [
        line for line in text.splitlines() if "HIVE_PICKER_HIDDEN_MODEL_IDS:" in line
    ]
    assert len(assignments) == 1, (
        "expected exactly one HIVE_PICKER_HIDDEN_MODEL_IDS assignment in "
        f"{COMPOSE.name}, found {len(assignments)}"
    )
    for alias in NON_CHAT_ALIASES:
        assert alias in assignments[0], f"{alias} is not hidden from the chat picker"
    for alias in CHAT_ALIASES:
        assert alias not in assignments[0], (
            f"{alias} is a chat model and must stay selectable in the picker"
        )


def test_bypass_and_picker_filter_stay_coupled() -> None:
    """The invariant #792 is about.

    While the bypass is on, Open WebUI's own access control cannot hide
    anything from anyone, so the picker fix has to be something else. If a
    future change turns the bypass off, this stops applying and whoever does it
    has to come here and re-derive it deliberately rather than inherit a
    silently dead mechanism.
    """
    text = COMPOSE.read_text(encoding="utf-8")
    bypass_on = 'BYPASS_MODEL_ACCESS_CONTROL: "true"' in text
    if not bypass_on:
        return
    _, invoked = dockerfile_runs_patch(DOCKERFILE, "apply_model_picker_patch.py")
    assert invoked, (
        "docker-compose.yml sets BYPASS_MODEL_ACCESS_CONTROL: \"true\", which "
        "disables Open WebUI's per-model access control for every role, so an "
        "access_control-shaped fix cannot hide the non-chat aliases from the "
        "picker (this is #792). A picker filter that does not depend on access "
        "control must be present and must run in the image."
    )


# --------------------------------------------------------------------------
# 4. Live: the check a unit test cannot make
# --------------------------------------------------------------------------

def _get_json(base: str, path: str, token: str):
    req = urllib.request.Request(
        base.rstrip("/") + path, headers={"Authorization": f"Bearer {token}"}
    )
    with urllib.request.urlopen(req, timeout=30) as response:
        return json.loads(response.read())


def run_live(base: str, token: str) -> None:
    """Assert both halves against a booted Open WebUI.

    The gateway list is read through Open WebUI's own /openai/models, which is
    the unfiltered upstream response, so this compares the two lists from one
    running system in one moment rather than trusting either in isolation.
    """
    gateway = {m["id"] for m in _get_json(base, "/openai/models", token)["data"]}
    picker = {m["id"] for m in _get_json(base, "/api/models", token)["data"]}

    missing = [a for a in NON_CHAT_ALIASES if a not in gateway]
    assert not missing, (
        f"the gateway stopped serving {missing}. The OpenAI-compatible model "
        "list must keep every alias; only the chat picker hides them."
    )
    leaked = sorted(a for a in NON_CHAT_ALIASES if a in picker)
    assert not leaked, (
        f"the chat picker still lists {leaked} (issue #792). The picker filter "
        "is not on the response path, or something is gating it."
    )
    assert picker, (
        "the chat picker is empty, which is a worse failure than the one being "
        "fixed and is indistinguishable from a gateway rejection (#717)."
    )
    print(
        f"live: gateway serves {len(gateway)} models including all "
        f"{len(NON_CHAT_ALIASES)} non-chat aliases; picker lists "
        f"{len(picker)} and none of them"
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", metavar="BASE_URL", help="booted Open WebUI base URL")
    parser.add_argument("--token", help="a signed-in Open WebUI bearer token")
    args = parser.parse_args()

    failures = []
    tests = [value for name, value in sorted(globals().items()) if name.startswith("test_")]
    for test in tests:
        try:
            test()
        except AssertionError as exc:
            failures.append(f"{test.__name__}: {exc}")

    if args.live:
        if not args.token:
            parser.error("--live needs --token")
        try:
            run_live(args.live, args.token)
        except (AssertionError, urllib.error.URLError, KeyError) as exc:
            failures.append(f"run_live: {exc}")

    if failures:
        print(f"OWUI chat model picker filter regression ({len(failures)} failure(s)):")
        for line in failures:
            print(f"  {line}")
        return 1

    print(f"OWUI chat model picker filter: {len(tests)} checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
