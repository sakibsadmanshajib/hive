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

The session DSN additionally carries `pool_max_conn_idle_time` and
`pool_health_check_period`. A cap alone bounds how many slots a consumer may
take; it does nothing about how long it keeps them. pgxpool holds an idle
connection for 30 minutes by default, so a consumer that burst to its cap once
squats that many session slots for the next half hour while running no queries
at all, and the ceiling is reached by consumers that are all idle. These two
parameters release an idle connection about a minute after its last use instead.

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

# A session slot is held for as long as the pgxpool connection that owns it
# exists, not for as long as it is in use, and pgxpool's own defaults are
# `pool_max_conn_idle_time` 30 minutes with a `pool_health_check_period` of 1
# minute. So any consumer that ever burst to `pool_max_conns` keeps that many of
# the 15 slots pinned for the next half hour of doing nothing at all, and three
# mostly-idle consumers (the demo box, a developer stack, one CI job) can hold
# the whole ceiling between them while none of them is running a query.
#
# Measured on 2026-08-10 with no CI running and one 30-minute-old local stack
# up: six parallel session-mode connections were all refused with
# EMAXCONNSESSION, so the pool was fully squatted by idle connections.
#
# Releasing an idle connection after a minute turns "pinned for 30 minutes" into
# "pinned while actually working", which is the resource CI and the live stack
# are really competing for. The health check period is the reaper's own tick, so
# it bounds how late that release can be: 60s + 15s worst case, against 30m + 1m
# before. Both are deliberately knobs rather than inlined literals: a deployment
# under steady traffic may prefer a longer idle window to avoid re-handshaking,
# and an explicit value in the input DSN still wins over both (see with_params).
SESSION_MAX_CONN_IDLE_TIME = "60s"
SESSION_HEALTH_CHECK_PERIOD = "15s"

# Transaction-mode clients are multiplexed, so this bound is only about not
# stampeding the upstream server connections.
TRANSACTION_MAX_CONNS = 8

# pgx reads these out of the DSN itself. libpq has no such connection options and
# fails the whole connection on an unknown one, so every non-pgx consumer
# (psycopg2 in Open WebUI, psql in the purge job) needs them removed.
PGX_ONLY_PARAMS = (
    "pool_max_conns",
    "default_query_exec_mode",
    "pool_max_conn_idle_time",
    "pool_health_check_period",
)


def is_pooler_host(host: str) -> bool:
    """Report whether host is a Supavisor pooler, which is what has two ports."""
    return host.lower().endswith(".pooler.supabase.com")


def encode_query(pairs: list[tuple[str, str]] | dict[str, str]) -> str:
    """Re-encode query pairs the way a Postgres connection URI expects.

    `parse_qsl` decodes `%20` to a space, and `urlencode` defaults to form
    encoding, which would emit that space as `+`. A Postgres URI is percent
    encoded, not form encoded: libpq passes `+` through as a literal plus, so
    `options=-c%20statement_timeout%3D3000` would round-trip into an options
    string the server cannot parse. `quote` keeps it as `%20`.
    """
    return urlencode(pairs, quote_via=quote)


def with_params(dsn: str, **params: str) -> str:
    """Return dsn with params set, preserving any the caller already supplied."""
    parts = urlsplit(dsn)
    query = dict(parse_qsl(parts.query, keep_blank_values=True))
    for key, value in params.items():
        # An explicit value already in the DSN wins: the deployment knows
        # something this script does not.
        query.setdefault(key, value)
    return urlunsplit(parts._replace(query=encode_query(query)))


def without_params(dsn: str, *keys: str) -> str:
    """Return dsn with keys removed, including any the input already carried."""
    parts = urlsplit(dsn)
    query = [(k, v) for k, v in parse_qsl(parts.query, keep_blank_values=True) if k not in keys]
    return urlunsplit(parts._replace(query=encode_query(query)))


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

    session = with_params(
        dsn,
        pool_max_conns=str(SESSION_MAX_CONNS),
        pool_max_conn_idle_time=SESSION_MAX_CONN_IDLE_TIME,
        pool_health_check_period=SESSION_HEALTH_CHECK_PERIOD,
    )

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
    # A cap without an idle release still lets one consumer squat six of the
    # fifteen session slots for pgxpool's default 30 idle minutes, which is how
    # three idle consumers exhaust the pool between them.
    assert "pool_max_conn_idle_time=60s" in session, session
    assert "pool_health_check_period=15s" in session, session
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

    # The two pool-lifetime parameters are pgx-only in exactly the way
    # pool_max_conns is, so an input DSN that already carries them must not
    # hand them to psql or psycopg2 either.
    _, _, idle_libpq = derive(pooler + "?pool_max_conn_idle_time=5s&pool_health_check_period=1s")
    assert "pool_max_conn_idle_time" not in idle_libpq, idle_libpq
    assert "pool_health_check_period" not in idle_libpq, idle_libpq

    # A password with URL-significant characters must not be mangled.
    built = build_dsn("h.pooler.supabase.com", "5432", "u", "postgres", "p@ss/w:rd?")
    assert "p%40ss%2Fw%3Ard%3F" in built, built
    _, built_txn, built_libpq = derive(built)
    assert "p%40ss%2Fw%3Ard%3F" in built_txn, built_txn
    assert "p%40ss%2Fw%3Ard%3F" in built_libpq, built_libpq

    # A percent-encoded parameter the deployment set must survive both the add
    # and the strip as percent encoding. Form encoding would emit the space in
    # `options` as `+`, which libpq passes through literally and the server then
    # cannot parse.
    encoded = pooler + "?options=-c%20statement_timeout%3D3000"
    enc_session, enc_transaction, enc_libpq = derive(encoded)
    for flavour in (enc_session, enc_transaction, enc_libpq):
        assert "options=-c%20statement_timeout%3D3000" in flavour, flavour
        assert "+" not in urlsplit(flavour).query, flavour

    print("derive-pooler-dsn: self-test ok (33 assertions)")
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
