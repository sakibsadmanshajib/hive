#!/usr/bin/env python3
"""Decide which supabase/migrations files are already applied to a database.

Why this exists
---------------
scripts/migration-baseline.conf tells scripts/apply-migrations.sh which
migrations ran before the ledger existed. That list has to come from evidence,
not from filenames: the drift on the demo database is interleaved, not a
prefix. 20260516_01 through _07 are applied while _08 and _09 are not, and
20260518_02 is applied while _01 and _04 are not.

An earlier version of the baseline was written from a spot check and asserted
in a comment that it had been probed exhaustively. That claim was false and a
review caught it. This script exists so the claim is reproducible: anyone can
re-run it and get the same verdicts, instead of trusting a comment.

Usage
-----
    scripts/probe-applied-migrations.py                  print the verdict table
    scripts/probe-applied-migrations.py --emit-baseline  print baseline conf body

Connection settings come from libpq environment variables (PGHOST, PGPORT,
PGUSER, PGDATABASE, PGPASSWORD), the same as scripts/apply-migrations.sh. Point
PGPORT at the pooler's TRANSACTION mode port (6543), never session mode: this
runs a batch of catalog queries and session-mode clients are capped at
pool_size 15, shared with CI, agents and the live chat surface. A read only
probe needs nothing transaction mode drops.

Every query it runs is read only. It never writes.

What counts as evidence
-----------------------
For each file the SQL is parsed with comments and dollar quoted bodies removed,
so that commented out DDL and DDL built dynamically inside a DO block are not
mistaken for real DDL, and every STRONG object the file creates is extracted:
tables, indexes, policies, types, triggers, added columns, added constraints,
extensions, enum values and views. A file is APPLIED only if every one of those
is present.

CREATE OR REPLACE functions and procedures are WEAK evidence and never prove a
file applied, because a later migration replacing the same function would
otherwise vouch for an earlier one.

Verdicts: APPLIED, UNAPPLIED (has strong objects, none present), PARTIAL (some
present, some not), UNPROVABLE (nothing checkable). Only APPLIED is treated as
applied. Everything else stays pending, because unknown must never resolve to
applied: a wrongly applied mark is a permanent silent skip, a wrongly unapplied
mark is a loud abort you can see and fix.

Known limits, which is why the verdict is evidence and not proof: existence is
checked, definition is not, so an object that exists with a different type,
default, predicate or column set still counts. Grants, ownership, column
defaults, comments, storage parameters and RLS enable flags are not checked,
and row data is never checked.
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MIGRATIONS_DIR = os.path.join(REPO_ROOT, "supabase", "migrations")


def psql(sql: str) -> list[str]:
    """Run one read-only query and return its rows. Fails loudly."""
    out = subprocess.run(
        ["psql", "--no-psqlrc", "-qtAX", "-v", "ON_ERROR_STOP=1", "-c", sql],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        sys.exit(f"psql failed: {out.stderr.strip()}")
    return [line for line in out.stdout.splitlines() if line]


def load_catalog() -> dict[str, set[str]]:
    """One batch of catalog reads, rather than a query per migration."""
    psql("SELECT 1")
    return {
        "table": set(psql(
            "SELECT lower(c.relname) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
            "WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f')")),
        "index": set(psql("SELECT lower(indexname) FROM pg_indexes WHERE schemaname='public'")),
        "policy": set(psql("SELECT lower(policyname) FROM pg_policies WHERE schemaname='public'")),
        "type": set(psql(
            "SELECT lower(t.typname) FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace "
            "WHERE n.nspname='public'")),
        "trigger": set(psql("SELECT lower(tgname) FROM pg_trigger WHERE NOT tgisinternal")),
        "constraint": set(psql(
            "SELECT lower(conname) FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace "
            "WHERE n.nspname='public'")),
        "extension": set(psql("SELECT lower(extname) FROM pg_extension")),
        "column": set(psql(
            "SELECT lower(table_name)||'.'||lower(column_name) FROM information_schema.columns "
            "WHERE table_schema='public'")),
        "enum": set(psql(
            "SELECT lower(t.typname)||':'||e.enumlabel FROM pg_enum e "
            "JOIN pg_type t ON t.oid=e.enumtypid")),
    }


def strip_sql(text: str) -> str:
    text = re.sub(r"/\*.*?\*/", " ", text, flags=re.S)
    text = re.sub(r"--[^\n]*", " ", text)
    # Dollar quoted bodies are removed so dynamically built DDL inside DO blocks
    # is not read as real DDL. Files whose only DDL lives there fall to
    # UNPROVABLE and stay pending, which is the safe direction.
    return re.sub(r"\$([A-Za-z_]*)\$.*?\$\1\$", " ", text, flags=re.S)


IN = r"(?:IF\s+NOT\s+EXISTS\s+)?"
NAME = r'([A-Za-z0-9_."]+)'
STRONG = [
    ("table", re.compile(rf"CREATE\s+(?:UNLOGGED\s+)?TABLE\s+{IN}{NAME}", re.I)),
    ("index", re.compile(rf"CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?{IN}{NAME}\s+ON", re.I)),
    ("policy", re.compile(rf"CREATE\s+POLICY\s+{NAME}\s+ON", re.I)),
    ("type", re.compile(rf"CREATE\s+TYPE\s+{NAME}", re.I)),
    ("trigger", re.compile(rf"CREATE\s+(?:OR\s+REPLACE\s+)?(?:CONSTRAINT\s+)?TRIGGER\s+{NAME}", re.I)),
    ("column", re.compile(rf"ALTER\s+TABLE\s+{IN}{NAME}\s+ADD\s+COLUMN\s+{IN}{NAME}", re.I)),
    ("constraint", re.compile(rf"ADD\s+CONSTRAINT\s+{NAME}", re.I)),
    ("extension", re.compile(rf"CREATE\s+EXTENSION\s+{IN}{NAME}", re.I)),
    ("enum", re.compile(rf"ALTER\s+TYPE\s+{NAME}\s+ADD\s+VALUE\s+{IN}'([^']+)'", re.I)),
    ("view", re.compile(rf"CREATE\s+(?:OR\s+REPLACE\s+)?(?:MATERIALIZED\s+)?VIEW\s+{IN}{NAME}", re.I)),
]
WEAK_FUNC = re.compile(rf"CREATE\s+(?:OR\s+REPLACE\s+)?(?:FUNCTION|PROCEDURE)\s+{NAME}", re.I)

# A later migration may legitimately drop something an earlier one created, in
# which case the earlier migration DID run and its object is simply gone. Without
# this, existence probing reports a false PARTIAL and the earlier file would be
# re-run, aborting on the objects that do still exist. Real case: 20260516_05
# creates index audit_outbox_undelivered and 20260518_01 drops it.
DROPPED = re.compile(
    r"DROP\s+(?:TABLE|INDEX|POLICY|TYPE|TRIGGER|VIEW|CONSTRAINT|EXTENSION)\s+"
    rf"(?:CONCURRENTLY\s+)?(?:IF\s+EXISTS\s+)?{NAME}", re.I)


def unqualify(name: str) -> str:
    return name.replace('"', "").split(".")[-1].lower()


def unprovable_reason(sql: str, raw: str) -> str:
    """Why a file had nothing checkable. This is what decides whether
    re-running it is safe, so it is recorded per entry rather than left as a
    bare label."""
    bits = []
    if WEAK_FUNC.search(sql):
        bits.append("CREATE OR REPLACE function or procedure only, which is weak evidence")
    if re.search(r"\$([A-Za-z_]*)\$", raw):
        bits.append("DDL built inside a dollar quoted block, invisible to the parser")
    if re.search(r"^\s*(INSERT|UPDATE|DELETE)\b", sql, re.I | re.M):
        bits.append("data only, and row data is never probed")
    if re.search(r"\b(GRANT|REVOKE)\b", sql, re.I):
        bits.append("grants only, which are not probed")
    if re.search(r"\bDROP\s+(CONSTRAINT|POLICY|INDEX|TRIGGER)\b", sql, re.I):
        bits.append("drops only, whose absence proves nothing")
    if re.search(r"\bALTER\s+TABLE\b.*\b(ENABLE|FORCE)\s+ROW\s+LEVEL\s+SECURITY", sql, re.I | re.S):
        bits.append("RLS enable or force flags only, which are not probed")
    if re.search(r"\bCOMMENT\s+ON\b", sql, re.I):
        bits.append("comments only, which are not probed")
    return "; ".join(bits) if bits else "no checkable statements found"


def classify(catalog: dict[str, set[str]]) -> list[tuple[str, str, str]]:
    files = sorted(f for f in os.listdir(MIGRATIONS_DIR) if f.endswith(".sql"))
    sources = {
        f: strip_sql(open(os.path.join(MIGRATIONS_DIR, f), encoding="utf-8").read())
        for f in files
    }
    # Names dropped by each file, so a later drop can excuse an earlier create.
    drops = {
        f: {unqualify(m.group(1)) for m in DROPPED.finditer(sources[f])} for f in files
    }

    results = []
    for position, name in enumerate(files):
        raw = open(os.path.join(MIGRATIONS_DIR, name), encoding="utf-8").read()
        sql = sources[name]
        dropped_later: set[str] = set()
        for later in files[position + 1:]:
            dropped_later |= drops[later]
        objects = []
        for kind, pattern in STRONG:
            for m in pattern.finditer(sql):
                if kind == "column":
                    key = f"{unqualify(m.group(1))}.{unqualify(m.group(2))}"
                elif kind == "enum":
                    key = f"{unqualify(m.group(1))}:{m.group(2)}"
                else:
                    key = unqualify(m.group(1))
                if key in dropped_later or key.split(".")[-1] in dropped_later:
                    continue
                lookup = catalog["table"] if kind == "view" else catalog[kind]
                objects.append((kind, key, key in lookup))

        if not objects:
            results.append((name, "UNPROVABLE", unprovable_reason(sql, raw)))
            continue
        missing = [o for o in objects if not o[2]]
        if not missing:
            results.append((name, "APPLIED", f"all {len(objects)} strong objects present"))
        elif len(missing) == len(objects):
            results.append((name, "UNAPPLIED",
                            f"0 of {len(objects)} present, first missing {missing[0][0]}:{missing[0][1]}"))
        else:
            detail = ", ".join(f"{k}:{n}" for k, n, _ in missing[:6])
            results.append((name, "PARTIAL",
                            f"{len(objects) - len(missing)} of {len(objects)} present, missing {detail}"))
    return results


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--emit-baseline", action="store_true",
                        help="print the applied= lines and the pending record block")
    args = parser.parse_args()

    results = classify(load_catalog())

    if args.emit_baseline:
        for name, verdict, _ in results:
            if verdict == "APPLIED":
                print(f"applied = {name}")
        print("\n# NOT listed above, therefore pending. Kept here as a record of why.")
        for name, verdict, reason in results:
            if verdict != "APPLIED":
                print(f"#   {verdict:<11} {name}")
                print(f"#     reason: {reason}")
        return

    for name, verdict, reason in results:
        print(f"{verdict:<11} {name}  [{reason}]")
    counts = {v: sum(1 for _, x, _ in results if x == v)
              for v in ("APPLIED", "UNAPPLIED", "PARTIAL", "UNPROVABLE")}
    print("\n" + ", ".join(f"{k}={v}" for k, v in counts.items()) + f", TOTAL={len(results)}")


if __name__ == "__main__":
    main()
