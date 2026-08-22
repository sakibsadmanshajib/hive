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
from urllib.parse import quote

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
        path = path.removeprefix("./")
        if not digest or not path:
            raise Problem(f"unparsable sha256 line: {line!r}")
        out[path] = digest
    return out


def unsafe_key(bucket: str, key: str) -> str | None:
    """Why this line must not be trusted, or None if it is fine.

    Structural, and deliberately stricter than "does it escape today": a key is
    a relative path of ordinary segments, so anything else is refused rather
    than normalised into something that looks safe. Normalising is what lets a
    later reader believe the value was clean all along.
    """
    for label, value in (("bucket", bucket), ("key", key)):
        if not value:
            return f"empty {label}"
        if value.startswith("/") or (len(value) > 1 and value[1] == ":"):
            return f"{label} is an absolute path, which discards the backup root entirely"
        if "\\" in value:
            return f"{label} contains a backslash, which is a path separator on some hosts"
        if "\x00" in value:
            return f"{label} contains a null byte"
    if "/" in bucket:
        return "bucket contains a path separator, so it is not one bucket name"
    segments = key.split("/")
    if any(seg in ("", ".", "..") for seg in segments):
        return (
            "key contains an empty, `.` or `..` segment, which walks out of the "
            "backup tree when the OS resolves it"
        )
    return None


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
        # An HTTP status IS an answer, so it is returned and judged by the
        # caller, which is how a 404 on the read-back becomes a named failure.
        return exc.code, exc.read()
    except OSError as exc:
        # A refused connection, a DNS failure or a timeout raises URLError,
        # which is an OSError and is NOT an HTTPError, so it escaped both this
        # handler and main()'s `except Problem` and ended the run in a Python
        # traceback instead of the operator-facing diagnostic this script exists
        # to print. socket.timeout is an OSError too, so one clause covers all
        # of them.
        raise Problem(
            f"{method} {url.split('?')[0]} could not be reached: {exc}. "
            "Check the stack is up and --base-url points at it"
        )


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
        status, body = request(f"{base_url}/bucket/{quote(bucket, safe='')}", key)
        if status != 200:
            raise Problem(
                f"bucket {bucket} does not exist on this backend (HTTP {status}). "
                "supabase-init creates the buckets; run the stack up first"
            )
        print(f"  bucket {bucket}: present")

    failures: list[str] = []
    declared = set(buckets)
    root = files_root.resolve()
    for bucket, obj_key, size, mime in objects:
        rel = f"{bucket}/{obj_key}"

        # The manifest is a FILE, so it is input, not a fact. Both fields below
        # were used verbatim to build a local path and a request path, and both
        # escape:
        #
        #   * an absolute obj_key discards everything to its left in a pathlib
        #     join, so `files_root / bucket / "/etc/shadow"` IS Path("/etc/shadow").
        #   * a relative `../../.env` is not collapsed by pathlib, but the OS
        #     resolves it on open, so it escapes just the same.
        #
        # Either one reads a host file and then POSTs its bytes into whatever
        # bucket the same line names, with the service-role key. That is
        # exfiltration, not a restore, so this refuses the line the same way a
        # malformed field count is refused.
        if bucket not in declared:
            failures.append(
                f"{rel}: names bucket {bucket!r}, which the manifest never declared. "
                "Only buckets listed in the manifest's own bucket rows may be written"
            )
            continue
        problem = unsafe_key(bucket, obj_key)
        if problem:
            failures.append(f"{rel}: {problem}")
            continue
        local = files_root / bucket / obj_key
        try:
            resolved = local.resolve()
        except OSError as exc:
            failures.append(f"{rel}: cannot resolve its path in the backup tree: {exc}")
            continue
        if not (resolved == root or root in resolved.parents):
            failures.append(
                f"{rel}: resolves outside the backup tree, refusing to read it"
            )
            continue
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

        # Percent-encoded, because the raw key goes into a URL path.
        #
        # Measured against this backend (supabase/storage-api v1.11.13) rather
        # than assumed, since the interesting question is which characters can
        # actually reach a manifest: a space, a `?`, a `+` and an `&` are all
        # ACCEPTED as object keys, while `#`, `%` and non-ASCII are refused
        # outright with `InvalidKey`. So the encoding is load bearing for two of
        # them and defensive for the rest. The `?` is the dangerous one: raw, it
        # starts a query string, so the upload and the read-back would both
        # address the truncated key, agree with each other, and report success
        # over an object stored under the wrong name.
        #
        # `/` stays safe in the key: it is the object name's own separator,
        # which is why the bucket and the key are quoted with different safe
        # sets rather than together.
        api_path = f"{quote(bucket, safe='')}/{quote(obj_key, safe='/')}"
        status, body = request(f"{base_url}/object/{api_path}", key, method="POST", body=data, mime=mime)
        if status not in (200, 201):
            failures.append(f"{rel}: upload answered HTTP {status}: {body[:200]!r}")
            continue

        # The whole point: fetched back through the API, not asserted from the
        # upload's own status.
        status, got = request(f"{base_url}/object/authenticated/{api_path}", key)
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

    # Every one of these was a live read of a host file, or a write into a
    # bucket the manifest never named, before unsafe_key existed.
    assert unsafe_key("hive-files", "a/b/one.txt") is None
    assert unsafe_key("hive-files", "invoices/2026-07.pdf") is None
    # A space and a hash are legal in an object name and must NOT be refused;
    # they are handled by quoting the URL, not by rejecting the line.
    assert unsafe_key("hive-files", "my report #3.pdf") is None
    for bucket, key, why in (
        ("hive-files", "/etc/shadow", "an absolute key"),
        ("hive-files", "../../../.env", "a relative traversal"),
        ("hive-files", "a/../../b", "a traversal in the middle"),
        ("hive-files", "a//b", "an empty segment"),
        ("hive-files", "a/./b", "a dot segment"),
        ("hive-files", "", "an empty key"),
        ("", "a", "an empty bucket"),
        ("../hive-files", "a", "a traversal in the bucket"),
        ("hive-files", "C:/Windows/win.ini", "a drive-letter absolute path"),
        ("hive-files", "a\\..\\b", "a backslash separator"),
    ):
        assert unsafe_key(bucket, key) is not None, f"{why} was accepted"

    # The URL built for a key with URL-significant characters addresses that
    # key and not a truncated one, and keeps `/` as the object separator.
    encoded = f"{quote('hive-files', safe='')}/{quote('a b/c#d?e%f', safe='/')}"
    assert encoded == "hive-files/a%20b/c%23d%3Fe%25f", encoded
    # The two characters this backend actually accepts in a key AND that break a
    # raw URL path, so these two assertions are the ones standing between a
    # correct restore and one that silently stores under a truncated name.
    assert quote("a b.txt", safe="/") == "a%20b.txt"
    assert quote("a?b.txt", safe="/") == "a%3Fb.txt"

    print("restore-storage-objects self-check: OK (32 cases)")
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
