#!/usr/bin/env python3
"""Restore Supabase Storage objects from a backup tree onto a self-hosted
Storage backend, and prove each one readable afterwards.

Why this is not a `cp` into a volume
------------------------------------
The self-hosted backend is `STORAGE_BACKEND=file`, so the bytes do live on a
volume, but a file written there directly is invisible: Storage answers every
read out of `storage.objects` and names the blob by that row's id, not by the
key. Copying files in produces a volume full of data and an API that 404s on
all of it, with no error anywhere. Uploading through the API is what makes the
row, the blob and the key one consistent thing, which is why this speaks HTTP.

Why the read-back is the point
------------------------------
An upload that answers 200 proves the request was accepted, not that the object
can be fetched. The two come apart for real: a mismatched bucket, an upsert that
replaced a row while orphaning its blob, a truncated body. So every object is
fetched back and its sha256 compared against the backup manifest, and the local
file's own sha256 is checked against that manifest BEFORE the upload, so a
corrupted backup is refused rather than faithfully published.

Usage
-----
    SUPABASE_SERVICE_ROLE_KEY=... restore-storage-objects.py <backup-dir>
    restore-storage-objects.py --self-check

The backup directory is the layout `pg_dump`-era cutover backups already use:

    storage-objects.txt     bucket|name|public          (3 fields, a bucket)
                            bucket|key|size|mimetype    (4 fields, an object)
    storage-files.sha256    sha256sum output, paths relative to storage-files/
    storage-files/<bucket>/<key>

Run it from the box's host shell. With no --base-url it resolves the Storage
container's own address with `docker inspect`, because the host cannot resolve
the compose service name `caddy-supabase` and the container publishes no port.
Idempotent: every upload sends `x-upsert: true`, so a re-run after a volume
reset is the intended way to use this.

The service-role key is read from the environment, never a flag, and is never
printed: an argv value is world readable in `ps` for the life of the process.
"""
from __future__ import annotations

import argparse
import hashlib
import os
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

STORAGE_CONTAINER = "hive-supabase-storage-1"
STORAGE_PORT = 5000


class Problem(Exception):
    """A refusal with a reason, never carrying a credential."""


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def parse_manifest(text: str) -> tuple[list[str], list[tuple[str, str, int, str]]]:
    """Split the manifest into (bucket ids, objects).

    Field count is what distinguishes the two kinds of row, and a row with any
    other shape is an error rather than something to skip: a silently dropped
    line is an object that never gets restored and never gets reported.
    """
    buckets: list[str] = []
    objects: list[tuple[str, str, int, str]] = []
    for lineno, line in enumerate(text.splitlines(), start=1):
        line = line.strip()
        if not line:
            continue
        parts = line.split("|")
        if len(parts) == 3:
            buckets.append(parts[0])
        elif len(parts) == 4:
            bucket, key, size, mime = parts
            try:
                size_int = int(size)
            except ValueError:
                raise Problem(f"manifest line {lineno}: size {size!r} is not a number")
            objects.append((bucket, key, size_int, mime))
        else:
            raise Problem(
                f"manifest line {lineno} has {len(parts)} fields, expected 3 for a bucket "
                "or 4 for an object"
            )
    return buckets, objects


def parse_sha_file(text: str) -> dict[str, str]:
    """`sha256sum` output into {relative path: digest}, `./` prefix removed."""
    out: dict[str, str] = {}
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        digest, _, path = line.partition(" ")
        path = path.strip().lstrip("*").strip()
        if path.startswith("./"):
            path = path[2:]
        if not digest or not path:
            raise Problem(f"unparsable sha256 line: {line!r}")
        out[path] = digest
    return out


def resolve_base_url() -> str:
    try:
        out = subprocess.run(
            [
                "docker",
                "inspect",
                "-f",
                "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
                STORAGE_CONTAINER,
            ],
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()
    except (OSError, subprocess.CalledProcessError) as exc:
        raise Problem(
            f"could not resolve {STORAGE_CONTAINER}'s address with docker inspect: {exc}. "
            "Pass --base-url instead"
        )
    if not out:
        raise Problem(f"{STORAGE_CONTAINER} reported no address; is the stack up?")
    return f"http://{out}:{STORAGE_PORT}"


def request(url: str, key: str, method: str = "GET", body: bytes | None = None,
            mime: str | None = None) -> tuple[int, bytes]:
    req = urllib.request.Request(url, data=body, method=method)
    req.add_header("Authorization", "Bearer " + key)
    if mime:
        req.add_header("Content-Type", mime)
    if method == "POST":
        req.add_header("x-upsert", "true")
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read()


def restore(backup_dir: Path, base_url: str, key: str) -> int:
    manifest = backup_dir / "storage-objects.txt"
    shafile = backup_dir / "storage-files.sha256"
    files_root = backup_dir / "storage-files"
    for path in (manifest, shafile, files_root):
        if not path.exists():
            raise Problem(f"backup is incomplete: {path} is missing")

    buckets, objects = parse_manifest(manifest.read_text())
    digests = parse_sha_file(shafile.read_text())
    print(f"manifest: {len(buckets)} buckets, {len(objects)} objects")

    # Buckets are asserted, never created: supabase-init owns their creation and
    # their public flag, and creating one here would be a second definition of
    # the same thing that can disagree with it.
    for bucket in buckets:
        status, body = request(f"{base_url}/bucket/{bucket}", key)
        if status != 200:
            raise Problem(
                f"bucket {bucket} does not exist on this backend (HTTP {status}). "
                "supabase-init creates the buckets; run the stack up first"
            )
        print(f"  bucket {bucket}: present")

    failures: list[str] = []
    for bucket, obj_key, size, mime in objects:
        rel = f"{bucket}/{obj_key}"
        local = files_root / bucket / obj_key
        if not local.exists():
            failures.append(f"{rel}: named in the manifest but absent from storage-files/")
            continue
        data = local.read_bytes()
        want = digests.get(rel)
        if want is None:
            failures.append(f"{rel}: no sha256 in storage-files.sha256, so nothing to verify against")
            continue
        local_sha = sha256_bytes(data)
        if local_sha != want:
            failures.append(
                f"{rel}: the backup file does not match its own manifest digest "
                f"({local_sha[:12]} vs {want[:12]}), refusing to publish a corrupted object"
            )
            continue
        if len(data) != size:
            failures.append(f"{rel}: local size {len(data)} does not match the manifest's {size}")
            continue

        status, body = request(f"{base_url}/object/{rel}", key, method="POST", body=data, mime=mime)
        if status not in (200, 201):
            failures.append(f"{rel}: upload answered HTTP {status}: {body[:200]!r}")
            continue

        # The whole point: fetched back through the API, not asserted from the
        # upload's own status.
        status, got = request(f"{base_url}/object/authenticated/{rel}", key)
        if status != 200:
            failures.append(f"{rel}: uploaded, but reading it back answered HTTP {status}")
            continue
        got_sha = sha256_bytes(got)
        if got_sha != want:
            failures.append(
                f"{rel}: read back with digest {got_sha[:12]}, expected {want[:12]}"
            )
            continue
        print(f"  {rel}: {len(data)} bytes, sha256 {want[:12]} verified by read-back")

    if failures:
        print("restore-storage-objects: FAIL")
        for f in failures:
            print("  - " + f)
        return 1
    print(f"restore-storage-objects: ok ({len(objects)} objects read back byte for byte)")
    return 0


SAMPLE_MANIFEST = """
hive-files|hive-files|f
hive-images|hive-images|f
hive-files|a/b/one.txt|3|text/plain
"""


def self_check() -> int:
    """The parsing and the digest comparison, which is everything here that can
    be wrong without a network."""
    buckets, objects = parse_manifest(SAMPLE_MANIFEST)
    assert buckets == ["hive-files", "hive-images"], buckets
    assert objects == [("hive-files", "a/b/one.txt", 3, "text/plain")], objects

    # A key containing a pipe would be split into the wrong number of fields
    # rather than silently truncated, and that has to be an error: a skipped
    # line is an object nobody notices is missing.
    for bad, why in (
        ("a|b", "a two-field line"),
        ("a|b|c|d|e", "a five-field line"),
        ("hive-files|k|notanumber|text/plain", "a non-numeric size"),
    ):
        try:
            parse_manifest(bad)
        except Problem:
            pass
        else:
            raise AssertionError(f"{why} was accepted")

    digests = parse_sha_file("abc  ./hive-files/a/b/one.txt\ndef  hive-images/x\n")
    assert digests == {"hive-files/a/b/one.txt": "abc", "hive-images/x": "def"}, digests
    try:
        parse_sha_file("nopath\n")
    except Problem:
        pass
    else:
        raise AssertionError("a digest line with no path was accepted")

    assert sha256_bytes(b"abc").startswith("ba7816bf"), sha256_bytes(b"abc")
    assert sha256_bytes(b"abd") != sha256_bytes(b"abc")

    print("restore-storage-objects self-check: OK (9 cases)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("backup_dir", nargs="?", help="the backup directory")
    parser.add_argument("--base-url", help="Storage API base, default: resolved with docker inspect")
    parser.add_argument("--self-check", action="store_true")
    args = parser.parse_args()

    if args.self_check:
        return self_check()
    if not args.backup_dir:
        parser.error("give a backup directory, or --self-check")

    key = os.environ.get("SUPABASE_SERVICE_ROLE_KEY", "").strip()
    if not key:
        print(
            "SUPABASE_SERVICE_ROLE_KEY is not set. It is read from the environment on "
            "purpose and has no flag: an argv value is world readable in ps."
        )
        return 1

    try:
        base = args.base_url or resolve_base_url()
        return restore(Path(args.backup_dir), base.rstrip("/"), key)
    except Problem as exc:
        print(f"restore-storage-objects: {exc}")
        return 1


if __name__ == "__main__":
    sys.exit(main())
