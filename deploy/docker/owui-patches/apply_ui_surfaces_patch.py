#!/usr/bin/env python3
"""Build-time rewrite of Open WebUI's prebuilt bundle to remove the chat
surfaces Hive does not ship (#772). See hive_ui_surfaces.py for why each
surface needs a bundle rewrite rather than a config flag.

Asserts its own effect, the same posture as this Dockerfile's other patches:
every rewrite must match its expected number of sites and leave none behind,
and the guard strings for the surfaces Hive keeps (Workspace > Knowledge, the
Admin Panel entry) must still be present afterwards. A future open-webui digest
whose bundle shifted therefore fails the build loudly instead of silently
restoring a removed surface or removing a kept one.
"""

import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

from hive_ui_surfaces import GUARDS, REWRITES, apply, verify_counts  # noqa: E402

BUNDLE = pathlib.Path("/app/build/_app/immutable")


def main() -> int:
    if not BUNDLE.is_dir():
        raise SystemExit(f"{BUNDLE} is missing -- open-webui image layout changed")

    totals = {rewrite.surface: 0 for rewrite in REWRITES}
    for path in sorted(BUNDLE.rglob("*.js")):
        original = path.read_text(encoding="utf-8", errors="strict")
        patched, hits = apply(original)
        for surface, count in hits.items():
            totals[surface] += count
        if patched != original:
            path.write_text(patched, encoding="utf-8")

    failures = verify_counts(totals)
    if failures:
        raise SystemExit(
            "open-webui bundle no longer matches hive_ui_surfaces.py; a removed "
            "surface may have come back:\n  " + "\n  ".join(failures)
        )

    # Re-read the tree once the writes are done: prove no rewrite target
    # survives anywhere, and that the kept surfaces were not caught by one.
    remaining = {rewrite.surface: 0 for rewrite in REWRITES}
    guards = {name: 0 for name, _ in GUARDS}
    for path in sorted(BUNDLE.rglob("*.js")):
        text = path.read_text(encoding="utf-8", errors="strict")
        for rewrite in REWRITES:
            remaining[rewrite.surface] += text.count(rewrite.find)
        for name, needle in GUARDS:
            guards[name] += text.count(needle)

    left = [surface for surface, count in remaining.items() if count]
    if left:
        raise SystemExit("rewrite did not stick for: " + ", ".join(sorted(left)))

    missing = [name for name, count in guards.items() if count != 1]
    if missing:
        raise SystemExit(
            "a surface Hive keeps was removed or moved, so the rewrites are no "
            "longer safe to trust: " + ", ".join(sorted(missing))
        )

    print(
        "hive: removed open-webui surfaces "
        + ", ".join(rewrite.surface for rewrite in REWRITES)
        + "; kept "
        + ", ".join(name for name, _ in GUARDS)
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
