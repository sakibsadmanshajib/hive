#!/usr/bin/env python3
"""Self-check for the audio error-leak patch (issue #1562).

The defect, captured in a browser against a running stack before this patch
existed (docs/proof/voice-response-format-1562/read-aloud-before-fix.png): a
failed read-aloud rendered this toast to the signed-in user.

    External: 500, message='Internal Server Error',
    url='http://edge-api:8080/v1/audio/speech'

Open WebUI's audio router stringifies the aiohttp exception into the HTTP
detail it returns to the browser, and aiohttp bakes the request URL into that
string. So the gateway's internal compose address was published to every chat
user on every voice failure. It is what the owner saw and reported as "the
voice mode actually calls edgeapi:8080/v1"; the browser never called it, it was
shown it.

This runs the real build-time patch against a copy of the vendored source and
asserts on the result. Structural, no framework, no network.
Run: python3 scripts/test_owui_audio_error_leak.py
"""

import ast
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
PATCH = REPO_ROOT / "deploy/docker/owui-patches/apply_audio_error_leak_patch.py"
VENDORED = REPO_ROOT / "vendor/open-webui/backend/open_webui/routers/audio.py"

# Every internal name a compose deployment could leak through this path. Ports
# included, because a bare service name is also a hostname on that network.
INTERNAL_HOSTS = [
    "edge-api:8080",
    "control-plane:8081",
    "litellm:4000",
    "open-webui:8080",
]


def run_patch(dest_dir: Path):
    dest = dest_dir / "audio.py"
    shutil.copy(VENDORED, dest)
    env = dict(os.environ)
    env["HIVE_OWUI_AUDIO_PY"] = str(dest)
    r = subprocess.run(
        [sys.executable, str(PATCH)], env=env, capture_output=True, text=True
    )
    return r, dest


def load_scrubber(patched_source: str):
    """Import just the scrubber out of the patched module.

    The module itself pulls in the whole Open WebUI backend, which is not
    installed here, so the function is extracted and executed on its own. That
    is the point: the assertions below run the REAL shipped code rather than a
    reimplementation of it that could agree with a broken original.
    """
    tree = ast.parse(patched_source)
    for node in tree.body:
        if isinstance(node, ast.FunctionDef) and node.name == "_hive_safe_detail":
            ns: dict = {}
            exec(compile(ast.Module([node], []), "<patched>", "exec"), {"re": re}, ns)
            return ns["_hive_safe_detail"]
    return None


def main() -> int:
    checks = {}

    with tempfile.TemporaryDirectory() as tmp:
        result, dest = run_patch(Path(tmp))
        checks["patch script exits 0"] = result.returncode == 0
        if result.returncode != 0:
            print(result.stdout)
            print(result.stderr)
            print("FAIL: patch script did not run")
            return 1

        patched = dest.read_text()

        checks["patched module still parses"] = _parses(patched)

        # The five sites that stringified an exception into a client-facing
        # detail. Both spellings, because the TTS helper names it `exc` and the
        # four transcription helpers name it `e`.
        checks["no raw exception reaches a client detail"] = (
            "External: {e}" not in patched and "External: {exc}" not in patched
        )

        scrub = load_scrubber(patched)
        checks["patch defines _hive_safe_detail"] = scrub is not None
        if scrub is None:
            return _report(checks)

        # The exact string aiohttp produced in the captured failure.
        leaked = (
            "500, message='Internal Server Error', "
            "url='http://edge-api:8080/v1/audio/speech'"
        )
        scrubbed = scrub(leaked)
        checks["the captured leak is scrubbed"] = "edge-api" not in scrubbed
        # Scrubbing must not throw the useful half away, or the next person
        # deletes the guard to get their diagnostics back.
        checks["the status and message survive scrubbing"] = (
            "500" in scrubbed and "Internal Server Error" in scrubbed
        )

        for host in INTERNAL_HOSTS:
            for template in (
                "connection refused to http://%s/v1/audio/speech",
                "Cannot connect to host %s ssl:default",
                "url='https://%s/v1/audio/transcriptions'",
            ):
                probe = template % host
                out = scrub(probe)
                name = host.split(":")[0]
                checks[f"scrubs {name} in: {template.split()[0]}"] = (
                    name not in out
                )

        # A message with nothing to hide passes through, so a provider-blind
        # message from edge-api still reaches the user intact.
        clean = "hive-tts request failed."
        checks["a clean message is unchanged"] = scrub(clean) == clean

    return _report(checks)


def _parses(text: str) -> bool:
    try:
        ast.parse(text)
        return True
    except SyntaxError:
        return False


def _report(checks) -> int:
    failed = [k for k, v in sorted(checks.items()) if not v]
    for k in sorted(checks):
        print(("PASS " if checks[k] else "FAIL ") + k)
    if failed:
        print("FAIL: " + ", ".join(failed))
        return 1
    print("OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
