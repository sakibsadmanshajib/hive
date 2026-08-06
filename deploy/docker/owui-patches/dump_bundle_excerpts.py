#!/usr/bin/env python3
"""Regenerate pinned-bundle-excerpts.json from the pinned Open WebUI image.

Why this file exists: every entry in hive_ui_surfaces.py is a verbatim
substring of a bundle that only exists inside the image, and PR CI never builds
that image (ci.yml runs `make test-scripts`; only owui-nightly.yml and
deploy-demo-box.yml build Dockerfile.open-webui). Without a checked-in sample of
the real bundle, a `find` edited into something that matches nothing passes
every test in the repo and only fails at deploy. The excerpts this writes are
real bundle bytes, so scripts/test_owui_ui_surfaces.py can check the table
against them in review.

Run it after any change to the pinned digest, and commit the result:

    cid=$(docker create <the digest pinned in Dockerfile.open-webui>)
    docker cp "$cid:/app/build/_app/immutable" /tmp/owui-bundle
    docker rm -f "$cid"
    python3 deploy/docker/owui-patches/dump_bundle_excerpts.py /tmp/owui-bundle

It refuses to write unless every rewrite and every guard matches exactly one
site, so a stale or wrong bundle cannot produce a fixture that then certifies
itself.
"""

import hashlib
import json
import pathlib
import re
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

from hive_ui_surfaces import GUARDS, REWRITES  # noqa: E402

HERE = pathlib.Path(__file__).resolve().parent
FIXTURE = HERE / "pinned-bundle-excerpts.json"
DOCKERFILE = HERE.parent / "Dockerfile.open-webui"
CONTEXT = 400


def pinned_image() -> str:
    match = re.search(r"^FROM\s+(\S+@sha256:[0-9a-f]{64})\s*$", DOCKERFILE.read_text(), re.M)
    if not match:
        raise SystemExit(f"{DOCKERFILE} does not pin its base image by digest")
    return match.group(1)


def locate(files: dict, needle: str, label: str) -> dict:
    found = [(path, text) for path, text in files.items() if needle in text]
    total = sum(text.count(needle) for _, text in found)
    if total != 1:
        raise SystemExit(
            f"{label}: expected exactly 1 site in the bundle, found {total}"
            f" in {[p for p, _ in found]}"
        )
    path, text = found[0]
    at = text.index(needle)
    start = max(0, at - CONTEXT)
    end = min(len(text), at + len(needle) + CONTEXT)
    return {
        "file": path,
        "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
        "offset": at,
        "text": text[start:end],
    }


def main(bundle_root: str) -> int:
    root = pathlib.Path(bundle_root)
    if not root.is_dir():
        raise SystemExit(f"{root} is not a directory")
    files = {
        path.relative_to(root).as_posix(): path.read_text(encoding="utf-8", errors="strict")
        for path in sorted(root.rglob("*.js"))
    }
    if not files:
        raise SystemExit(f"no .js files under {root}")

    payload = {
        "image": pinned_image(),
        "note": (
            "Verbatim excerpts of the pinned Open WebUI bundle, with surrounding "
            "context, so the rewrite table can be checked against real bytes in PR "
            "CI without building the image. Regenerate with dump_bundle_excerpts.py "
            "whenever the digest above changes."
        ),
        "context_chars": CONTEXT,
        "rewrites": {r.surface: locate(files, r.find, r.surface) for r in REWRITES},
        "guards": {name: locate(files, needle, name) for name, needle in GUARDS},
    }
    FIXTURE.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"wrote {FIXTURE} for {payload['image']}")
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit(f"usage: {sys.argv[0]} <extracted _app/immutable dir>")
    sys.exit(main(sys.argv[1]))
