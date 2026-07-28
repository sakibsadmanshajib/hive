#!/usr/bin/env python3
"""Derive the session-mode and transaction-mode Supabase pooler DSNs from one input.

Why this exists
---------------
`SUPABASE_DB_URL` points at Supavisor on port 5432, which is SESSION mode. In
session mode a client holds one server connection for its whole session, so the
number of clients that can connect at once equals the project's `pool_size`,
which is 15. Every consumer shares that one ceiling: the demo stack
(control-plane, edge-api, open-webui), every CI job that boots a stack, the
`Purge CI test data` job, and any one-off `psql`. When the ceiling is hit the
pooler answers `FATAL: (EMAXCONNSESSION) max clients reached in session mode`,
which has taken down the live chat surface, `deploy-demo-box`, the purge job and
the OWUI nightly.

Port 6543 is TRANSACTION mode. There, clients are multiplexed over the same
server connections and are instead bounded by the much higher per-tier
"max pooler clients" limit, so a transaction-mode consumer costs no session
slot at all. The catch is that transaction mode drops session-scoped features:
prepared statements, `LISTEN`/`NOTIFY` and session-scoped advisory locks.

So the split is by what a consumer actually needs, not by convenience:

* control-plane MUST stay session mode. It holds one connection permanently for
  `LISTEN tenant_settings_changed`, and `accounting.PgxAccountLocker` takes a
  session-scoped `pg_advisory_lock` that it holds across a whole credit
  reservation. Both break silently under transaction mode.
* edge-api, open-webui's pgvector store, the purge job and one-off tooling need
  none of that, so they belong on transaction mode. edge-api's DSN therefore
  carries `default_query_exec_mode=exec`, which stops pgx using prepared
  statements.

Both DSNs also carry an explicit `pool_max_conns`. Without it pgxpool defaults to
`max(4, NumCPU)`, so the session-mode budget silently tracks the host's core
count: two Go services on an 8-core box would reserve 16 of the 15 slots between
them and leave nothing for Open WebUI, which is exactly the observed failure.
An explicit cap makes the budget a property of the deployment rather than of
whatever hardware it lands on.

Usage
-----
    derive-pooler-dsn.py --dsn "postgresql://user:pw@host:5432/postgres"
    derive-pooler-dsn.py --host H --port 5432 --user U --dbname D --password P
    derive-pooler-dsn.py --self-test

Prints three `KEY=value` lines, ready to append to $GITHUB_ENV:

    SUPABASE_DB_URL=...              session mode, capped
    SUPABASE_DB_POOL_URL=...         transaction mode, capped, no prepared statements
    SUPABASE_DB_POOL_URL_LIBPQ=...   transaction mode, no pgx-only parameters

`pool_max_conns` and `default_query_exec_mode` are pgx's own DSN parameters, not
libpq connection options. libpq rejects the whole DSN on an unknown option, so
psycopg2 and psql get their own flavour of the transaction DSN with those two
stripped. Handing the pgx flavour to Open WebUI is what produced
`invalid dsn: invalid connection option "pool_max_conns"`, which crash-looped the
container and returned 502 on the live chat surface.

A host that is not a Supavisor pooler (a direct Postgres, as the enterprise
profile uses) has no transaction-mode port, so its port is left alone and both
values keep it. The cap still applies.
"""

from __future__ import annotations

import argparse
import sys
from urllib.parse import parse_qsl, quote, urlencode, urlsplit, urlunsplit

# Supavisor's fixed port pair. Session mode is 5432 only: Supabase deprecated
# session mode on 6543 in February 2025, so 6543 is unambiguously transaction
# mode.
SESSION_PORT = 5432
TRANSACTION_PORT = 6543

# Session slots are a hard 15 shared by everything, so control-plane gets a
# budget rather than the whole pool. Six covers the permanent LISTEN connection
# plus normal query traffic plus a held account advisory lock, and leaves room
# for a second control-plane instance and any transient psql.
SESSION_MAX_CONNS = 6

# Transaction-mode clients are multiplexed, so this bound is only about not
# stampeding the upstream server connections.
TRANSACTION_MAX_CONNS = 8

# pgx reads these out of the DSN itself. libpq has no such connection options and
# fails the whole connection on an unknown one, so every non-pgx consumer
# (psycopg2 in Open WebUI, psql in the purge job) needs them removed.
PGX_ONLY_PARAMS = ("pool_max_conns", "default_query_exec_mode")


def is_pooler_host(host: str) -> bool:
    """Report whether host is a Supavisor pooler, which is what has two ports."""
    return host.lower().endswith(".pooler.supabase.com")


def with_params(dsn: str, **params: str) -> str:
    """Return dsn with params set, preserving any the caller already supplied."""
    parts = urlsplit(dsn)
    query = dict(parse_qsl(parts.query, keep_blank_values=True))
    for key, value in params.items():
        # An explicit value already in the DSN wins: the deployment knows
        # something this script does not.
        query.setdefault(key, value)
    return urlunsplit(parts._replace(query=urlencode(query)))


def without_params(dsn: str, *keys: str) -> str:
    """Return dsn with keys removed, including any the input already carried."""
    parts = urlsplit(dsn)
    query = [(k, v) for k, v in parse_qsl(parts.query, keep_blank_values=True) if k not in keys]
    return urlunsplit(parts._replace(query=urlencode(query)))


def with_port(dsn: str, port: int) -> str:
    parts = urlsplit(dsn)
    if parts.hostname is None:
        raise ValueError(f"DSN has no host: {dsn!r}")
    netloc = ""
    if parts.username:
        netloc += parts.username
        if parts.password:
            netloc += f":{parts.password}"
        netloc += "@"
    host = parts.hostname
    if ":" in host:  # bare IPv6 literal
        host = f"[{host}]"
    netloc += f"{host}:{port}"
    return urlunsplit(parts._replace(netloc=netloc))


def derive(dsn: str) -> tuple[str, str, str]:
    """Return (session_dsn, transaction_dsn, transaction_libpq_dsn) for one DSN.

    The third value is the transaction DSN as libpq will accept it: same host,
    port and credential, with pgx's own parameters stripped.
    """
    parts = urlsplit(dsn)
    host = parts.hostname
    if not host:
        raise ValueError(f"DSN has no host: {dsn!r}")

    session = with_params(dsn, pool_max_conns=str(SESSION_MAX_CONNS))

    if not is_pooler_host(host):
        # A direct Postgres serves every mode on its one port. Capping is still
        # right; moving the port would just break the connection.
        transaction = with_params(dsn, pool_max_conns=str(TRANSACTION_MAX_CONNS))
        return session, transaction, without_params(transaction, *PGX_ONLY_PARAMS)

    session = with_port(session, SESSION_PORT)
    transaction = with_params(
        with_port(dsn, TRANSACTION_PORT),
        pool_max_conns=str(TRANSACTION_MAX_CONNS),
        # Transaction mode cannot carry a prepared statement across the
        # connection it was prepared on, and pgx caches prepared statements by
        # default. `exec` sends each query unprepared using the extended
        # protocol, which is what survives here.
        default_query_exec_mode="exec",
    )
    return session, transaction, without_params(transaction, *PGX_ONLY_PARAMS)


def build_dsn(host: str, port: str, user: str, dbname: str, password: str) -> str:
    return f"postgresql://{quote(user, safe='')}:{quote(password, safe='')}@{host}:{port}/{dbname}"


def self_test() -> int:
    pooler = "postgresql://postgres.abc:pw@aws-1-us-east-1.pooler.supabase.com:5432/postgres"
    session, transaction, libpq = derive(pooler)

    assert ":5432/" in session, session
    assert "pool_max_conns=6" in session, session
    # The session DSN must never disable prepared statements or otherwise
    # advertise transaction-mode settings.
    assert "default_query_exec_mode" not in session, session

    assert ":6543/" in transaction, transaction
    assert "pool_max_conns=8" in transaction, transaction
    assert "default_query_exec_mode=exec" in transaction, transaction

    # The credential must survive the port rewrite untouched.
    assert "postgres.abc:pw@" in transaction, transaction

    # libpq rejects a DSN outright on an unknown connection option, so the
    # psycopg2/psql flavour reaches the same port with neither pgx parameter.
    # Open WebUI got the pgx flavour once and answered
    # `invalid dsn: invalid connection option "pool_max_conns"`, crash-looped,
    # and took chat-hive.scubed.co to 502 with it.
    assert ":6543/" in libpq, libpq
    assert "postgres.abc:pw@" in libpq, libpq
    for pgx_only in PGX_ONLY_PARAMS:
        assert pgx_only not in libpq, libpq

    # An input already on the transaction port still yields a session DSN on
    # 5432, so a mis-set port cannot silently strand control-plane.
    session_from_6543, _, _ = derive(
        "postgresql://u:p@aws-1-us-east-1.pooler.supabase.com:6543/postgres"
    )
    assert ":5432/" in session_from_6543, session_from_6543

    # A direct Postgres keeps its port in all three.
    direct = "postgresql://postgres:pw@supabase-db:5432/postgres"
    d_session, d_transaction, d_libpq = derive(direct)
    assert ":5432/" in d_session and ":6543/" not in d_session, d_session
    assert ":5432/" in d_transaction and ":6543/" not in d_transaction, d_transaction
    assert ":5432/" in d_libpq and "pool_max_conns" not in d_libpq, d_libpq

    # An explicit pool_max_conns in the input is the deployment's call, not ours.
    pinned, _, pinned_libpq = derive(pooler + "?pool_max_conns=2")
    assert "pool_max_conns=2" in pinned, pinned
    assert "pool_max_conns=6" not in pinned, pinned
    # ...but libpq still cannot parse it, so the strip has to win over the pin.
    assert "pool_max_conns" not in pinned_libpq, pinned_libpq

    # A password with URL-significant characters must not be mangled.
    built = build_dsn("h.pooler.supabase.com", "5432", "u", "postgres", "p@ss/w:rd?")
    assert "p%40ss%2Fw%3Ard%3F" in built, built
    _, built_txn, built_libpq = derive(built)
    assert "p%40ss%2Fw%3Ard%3F" in built_txn, built_txn
    assert "p%40ss%2Fw%3Ard%3F" in built_libpq, built_libpq

    print("derive-pooler-dsn: self-test ok (18 assertions)")
    return 0


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--dsn")
    ap.add_argument("--host")
    ap.add_argument("--port", default=str(SESSION_PORT))
    ap.add_argument("--user")
    ap.add_argument("--dbname")
    ap.add_argument("--password")
    ap.add_argument("--self-test", action="store_true")
    args = ap.parse_args(argv)

    if args.self_test:
        return self_test()

    dsn = args.dsn
    if not dsn:
        missing = [
            name
            for name in ("host", "user", "dbname", "password")
            if not getattr(args, name)
        ]
        if missing:
            ap.error(f"--dsn, or all of --host --user --dbname --password (missing: {', '.join(missing)})")
        dsn = build_dsn(args.host, args.port, args.user, args.dbname, args.password)

    session, transaction, libpq = derive(dsn)
    print(f"SUPABASE_DB_URL={session}")
    print(f"SUPABASE_DB_POOL_URL={transaction}")
    print(f"SUPABASE_DB_POOL_URL_LIBPQ={libpq}")

    # Summary on stderr so a caller can redirect stdout straight into
    # $GITHUB_ENV and still get something readable in the log. Host, port and
    # parameters only: the DSN carries the database password.
    for name, value in (
        ("SUPABASE_DB_URL", session),
        ("SUPABASE_DB_POOL_URL", transaction),
        ("SUPABASE_DB_POOL_URL_LIBPQ", libpq),
    ):
        parts = urlsplit(value)
        print(
            f"{name}: host={parts.hostname} port={parts.port} params={parts.query}",
            file=sys.stderr,
        )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
