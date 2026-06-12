#!/usr/bin/env python3
"""Validate the staged bn-BD translation against the en-US source.

Checks:
  1. Both files are valid JSON objects.
  2. The bn-BD key set is identical to the en-US key set (no missing / extra keys).
  3. Every interpolation placeholder ({{...}} and {single-brace}) present in a key
     appears, verbatim and with the same multiplicity, in its translation value.
  4. No empty translation values remain (full coverage).

Usage:
    python3 validate_placeholders.py
    python3 validate_placeholders.py --en path/to/en-US.json --bn path/to/bn-BD.json

Exit code 0 on success, 1 on any failure.
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from pathlib import Path

HERE = Path(__file__).resolve().parent
PLACEHOLDER = re.compile(r"\{\{.*?\}\}|\{[^{}]+\}")


def load(path: Path) -> dict:
    with path.open(encoding="utf-8") as fh:
        data = json.load(fh)
    if not isinstance(data, dict):
        raise SystemExit(f"FAIL: {path} is not a JSON object")
    return data


def tokens(text: str) -> Counter:
    return Counter(PLACEHOLDER.findall(text))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--en", default=str(HERE / "en-US.reference.json"))
    parser.add_argument("--bn", default=str(HERE / "translation.json"))
    args = parser.parse_args()

    en = load(Path(args.en))
    bn = load(Path(args.bn))

    failures: list[str] = []

    # 2. key parity
    en_keys, bn_keys = set(en), set(bn)
    missing = en_keys - bn_keys
    extra = bn_keys - en_keys
    if missing:
        failures.append(f"{len(missing)} key(s) missing from bn-BD (sample: {sorted(missing)[:3]})")
    if extra:
        failures.append(f"{len(extra)} extra key(s) in bn-BD (sample: {sorted(extra)[:3]})")

    # 3. placeholder parity (a key is its own English source string in OWUI i18n)
    placeholder_mismatches = []
    for key, value in bn.items():
        src = tokens(key)
        tgt = tokens(value)
        if src != tgt:
            placeholder_mismatches.append((key, value, dict(src), dict(tgt)))
    if placeholder_mismatches:
        failures.append(f"{len(placeholder_mismatches)} placeholder mismatch(es)")

    # 4. coverage
    empty = [k for k, v in bn.items() if not str(v).strip()]
    if empty:
        failures.append(f"{len(empty)} empty translation value(s) remain")

    # report
    print("Open WebUI bn-BD translation validation")
    print(f"  en-US keys           : {len(en_keys)}")
    print(f"  bn-BD keys           : {len(bn_keys)}")
    print(f"  key set identical    : {en_keys == bn_keys}")
    print(f"  placeholder mismatch : {len(placeholder_mismatches)}")
    print(f"  empty values         : {len(empty)}")
    print(f"  coverage             : {100 * (len(bn) - len(empty)) / len(bn):.1f}%")

    if placeholder_mismatches:
        print("\nPlaceholder mismatches:")
        for key, value, src, tgt in placeholder_mismatches[:20]:
            print(f"  KEY  : {key!r}")
            print(f"  VALUE: {value!r}")
            print(f"  en={src}  bn={tgt}")

    if failures:
        print("\nRESULT: FAIL")
        for f in failures:
            print(f"  - {f}")
        return 1

    print("\nRESULT: PASS (valid JSON, identical key set, zero placeholder mismatches, full coverage)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
