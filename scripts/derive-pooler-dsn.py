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

The session cap defaults to the EPHEMERAL budget, and `--session-max-conns`
raises it for the one long-lived deployment. See `DEFAULT_SESSION_MAX_CONNS`
below for the arithmetic: with the old single value of six, one push asked for
18 of the 15 slots across its three CI stacks, and the visible symptom was
unrelated browser specs passing only on a retry.

That split reduces demand; it does not create headroom, and nothing in this
file can. Three ephemeral stacks at four plus the long-lived six is still 18
against a ceiling of 15, so two pushes landing together can still exhaust the
pool. The changes that add capacity are raising the project's Supavisor
`pool_size` (the database itself has room: `max_connections` is 60) or moving
CI onto its own project, which are issues #841 and #631. Serialising the jobs
instead was tried and reverted, because a GitHub Actions concurrency group
holds only one queued run and cancels the rest, which trades a noisy failure
for a silent absence.

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
    derive-pooler-dsn.py --dsn "..." --session-max-conns 6
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
from urllib.parse import parse_qsl, quote, unquote, urlencode, urlsplit, urlunsplit

# Supavisor's fixed port pair. Session mode is 5432 only: Supabase deprecated
# session mode on 6543 in February 2025, so 6543 is unambiguously transaction
# mode.
SESSION_PORT = 5432
TRANSACTION_PORT = 6543

# Session slots are a hard 15 shared by everything, so control-plane gets a
# budget rather than the whole pool.
#
# The default is the EPHEMERAL budget, and that is the important part. Six was
# the original value and it was sized for one long-lived deployment; every
# ephemeral consumer inherited it. Three stacks boot per push (ci.yml's web-e2e
# job, ci.yml's live-integration job, agent-visual-proof.yml), so a single push
# asked for 18 of the 15 slots before the always-on demo box's own six was
# counted. On 2026-08-16 that produced 258 `EMAXCONNSESSION` refusals inside one
# Web E2E job, whose visible symptom was three unrelated specs passing only on a
# retry and the repository's flake gate failing `main` for it.
#
# Four, and the arithmetic behind it is the reservation path rather than a round
# number. `accounting.PgxAccountLocker.WithAccountLock` checks out one connection
# per account currently holding the advisory lock and then runs `fn`, which needs
# a further connection for its own ledger and usage writes; `pglock.go` says so
# in its own comment and gates per account in-process for exactly this reason. So
# a pool of `max` survives `max - 2` concurrent account locks: one connection is
# pinned for the life of the process by `LISTEN tenant_settings_changed`, K are
# held by lock holders, and at least one has to stay free or the holders starve
# and the reservation path deadlocks until their contexts expire. Four leaves
# room for two concurrent accounts, which is above anything CI drives; three
# would allow exactly one, and trading a contention timeout for a starvation
# timeout is not a fix.
#
# A deployment that needs more asks for it with --session-max-conns, so the large
# budget is explicit and local to the one consumer that earned it, and a new
# workflow that forgets the flag fails safe.
DEFAULT_SESSION_MAX_CONNS = 4

# Below two, the listener's permanently checked-out connection leaves nothing for
# queries and control-plane wedges on its first one. Refuse it here, where the
# message can say why.
MIN_SESSION_MAX_CONNS = 2

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


def normalize_scheme(dsn: str) -> str:
    """Return dsn with the `postgres://` scheme rewritten to `postgresql://`.

    libpq and pgx accept both spellings, so a DSN written either way reaches
    control-plane and edge-api intact and nothing here ever noticed the
    difference. SQLAlchemy accepts only `postgresql://`: it looks the scheme up
    in its dialect registry and `postgres` was removed from that registry in
    SQLAlchemy 1.4. Open WebUI's pgvector store is a SQLAlchemy consumer of the
    libpq flavour below, so a `postgres://` DSN reaches it as
    `NoSuchModuleError: Can't load plugin: sqlalchemy.dialects:postgres` and the
    container never becomes healthy. Observed on the demo box during the
    self-hosted Supabase cutover, from an operator-written DSN using the short
    spelling.

    Normalising here rather than at each consumer is the one place all three
    derived DSNs pass through, and the rewrite is a no-op for the two drivers
    that already accepted both.
    """
    parts = urlsplit(dsn)
    if parts.scheme == "postgres":
        return urlunsplit(parts._replace(scheme="postgresql"))
    return dsn


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


def derive(
    dsn: str, session_max_conns: int = DEFAULT_SESSION_MAX_CONNS
) -> tuple[str, str, str]:
    """Return (session_dsn, transaction_dsn, transaction_libpq_dsn) for one DSN.

    The third value is the transaction DSN as libpq will accept it: same host,
    port and credential, with pgx's own parameters stripped.

    session_max_conns is this consumer's share of the project's 15 session slots.
    It defaults to the ephemeral budget; a long-lived deployment passes its own.
    """
    dsn = normalize_scheme(dsn)
    parts = urlsplit(dsn)
    host = parts.hostname
    if not host:
        raise ValueError(f"DSN has no host: {dsn!r}")

    session = with_params(
        dsn,
        pool_max_conns=str(session_max_conns),
        pool_max_conn_idle_time=SESSION_MAX_CONN_IDLE_TIME,
        pool_health_check_period=SESSION_HEALTH_CHECK_PERIOD,
    )

    # Validate what the session DSN ENDS UP carrying, not the argument. An
    # explicit `pool_max_conns` in the input wins over this script's value (see
    # with_params), so checking only `session_max_conns` would let an input DSN
    # of `?pool_max_conns=1` through and wedge control-plane on its first query:
    # the tenant-settings listener would hold the single connection for the life
    # of the process and nothing would be left to run anything on.
    effective = dict(parse_qsl(urlsplit(session).query)).get("pool_max_conns", "")
    if not effective.isdigit() or int(effective) < MIN_SESSION_MAX_CONNS:
        raise ValueError(
            f"effective session pool_max_conns={effective!r} is unusable: it must be an "
            f"integer of at least {MIN_SESSION_MAX_CONNS}, because control-plane pins one "
            "pool connection for the life of the process on LISTEN "
            "tenant_settings_changed and needs at least one more to run queries on. The "
            "value comes from the input DSN if it carries one, otherwise from "
            "--session-max-conns"
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


# libpq's own names for what a DSN carries. psql is a libpq client and so are
# apply-migrations.sh and check-retention-schedule.sh, both of which take their
# connection from these variables and deliberately never parse a DSN.
LIBPQ_ENV_ORDER = ("PGHOST", "PGPORT", "PGUSER", "PGDATABASE", "PGPASSWORD")


def libpq_env(libpq_dsn: str) -> dict[str, str]:
    """Split a libpq-safe DSN into the libpq environment variables.

    This exists so a caller that must hand a script libpq variables, rather
    than a DSN, still derives them from the one DSN the deployment actually
    uses instead of assembling its own host and port from somewhere else. That
    "somewhere else" is the whole defect: the migrate job used to read a
    separate set of secrets, which after the self-hosted cutover pointed at a
    different database than the stack, and reported success either way.

    A query parameter with no libpq environment equivalent is refused rather
    than dropped. Silently losing, say, sslmode from a migration connection is
    the same class of quiet difference this function exists to remove.
    """
    parts = urlsplit(normalize_scheme(libpq_dsn))
    host = parts.hostname
    if not host:
        raise ValueError(f"DSN has no host: {libpq_dsn!r}")
    dbname = parts.path.lstrip("/")
    if not dbname:
        raise ValueError(f"DSN has no database name: {libpq_dsn!r}")
    leftover = [name for name, _ in parse_qsl(parts.query)]
    if leftover:
        raise ValueError(
            f"DSN carries parameters with no libpq environment equivalent: "
            f"{', '.join(sorted(leftover))}. Export the matching PG* variable "
            "explicitly instead of relying on this conversion, which would "
            "otherwise drop them silently"
        )
    # urlsplit leaves the userinfo percent-encoded, and libpq environment
    # variables are raw values. Handing psql `p%40ss` as PGPASSWORD
    # authenticates as the literal string, which fails with a password error
    # that says nothing about encoding.
    return {
        "PGHOST": host,
        "PGPORT": str(parts.port or SESSION_PORT),
        "PGUSER": unquote(parts.username or ""),
        "PGDATABASE": dbname,
        "PGPASSWORD": unquote(parts.password or ""),
    }


def build_dsn(host: str, port: str, user: str, dbname: str, password: str) -> str:
    return f"postgresql://{quote(user, safe='')}:{quote(password, safe='')}@{host}:{port}/{dbname}"


def self_test() -> int:
    pooler = "postgresql://postgres.abc:pw@aws-1-us-east-1.pooler.supabase.com:5432/postgres"
    session, transaction, libpq = derive(pooler)

    assert ":5432/" in session, session
    # The default is the ephemeral budget, so a consumer that asks for nothing
    # gets the small one instead of a third of the project's whole session
    # ceiling. Never below three: see DEFAULT_SESSION_MAX_CONNS for why a pool
    # of `max` only survives `max - 2` concurrent account locks.
    assert "pool_max_conns=4" in session, session
    assert DEFAULT_SESSION_MAX_CONNS >= 3, DEFAULT_SESSION_MAX_CONNS

    # A long-lived deployment asks for its larger budget explicitly.
    big_session, big_transaction, big_libpq = derive(pooler, session_max_conns=6)
    assert "pool_max_conns=6" in big_session, big_session
    # The transaction flavours are not session slots and must not move with it.
    assert "pool_max_conns=8" in big_transaction, big_transaction
    assert "pool_max_conns" not in big_libpq, big_libpq

    # settings.Resolver.StartListener acquires a pool connection and holds it for
    # the life of the process, so a cap of 1 leaves zero connections for queries
    # and control-plane wedges on its first request. Refuse it here, where the
    # message can say why, rather than in a container that merely hangs.
    for rejected in (1, 0, -1):
        try:
            derive(pooler, session_max_conns=rejected)
        except ValueError:
            pass
        else:
            raise AssertionError(f"session_max_conns={rejected} must be rejected")

    # The floor has to hold on the EFFECTIVE value. An explicit parameter in the
    # input DSN outranks this script's own (see with_params), so a DSN that pins
    # an unusable cap must be refused even though the flag was never touched, and
    # a non-numeric value must not reach pgx as a live DSN either.
    for bad_input in ("?pool_max_conns=1", "?pool_max_conns=0", "?pool_max_conns=lots"):
        try:
            derive(pooler + bad_input)
        except ValueError:
            pass
        else:
            raise AssertionError(f"input DSN {bad_input} must be rejected")
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

    # The short `postgres://` spelling is normalised in ALL THREE outputs.
    # libpq and pgx accept it, so nothing downstream of them ever complained;
    # SQLAlchemy removed `postgres` from its dialect registry in 1.4, and Open
    # WebUI's pgvector store consumes the libpq flavour, so the short spelling
    # reached it as NoSuchModuleError and the container never went healthy.
    # `${PASSWORD}` rather than a short literal on purpose: a secret scanner
    # reads `postgres:pw@host` as PostgreSQL credentials and reports the diff,
    # and a scanner that cries wolf on a fixture is a scanner people learn to
    # ignore. Nothing here parses the password, only the scheme and the host.
    short = "postgres://postgres:${PASSWORD}@supabase-db:5432/postgres"
    s_session, s_transaction, s_libpq = derive(short)
    for flavour in (s_session, s_transaction, s_libpq):
        assert flavour.startswith("postgresql://"), flavour
    # Nothing else about the DSN moves: same host, port, credential, database.
    assert "@supabase-db:5432/postgres" in s_libpq, s_libpq
    # The long spelling is untouched, so this is a normalisation and not a
    # rewrite that could mangle an already-correct DSN. Asserted against the
    # INPUT, not against another call: comparing derive(direct) with values
    # captured from derive(direct) proves only that derive is deterministic,
    # which it would be whatever normalize_scheme did to a long-spelling DSN.
    assert normalize_scheme(direct) == direct, normalize_scheme(direct)
    assert normalize_scheme(pooler) == pooler, normalize_scheme(pooler)
    # A pooler host normalises too, port move and all.
    short_pooler = pooler.replace("postgresql://", "postgres://", 1)
    for flavour in derive(short_pooler):
        assert flavour.startswith("postgresql://"), flavour

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

    # --emit-libpq-env carries every component through, and decodes the
    # password rather than handing psql a percent-encoded one.
    _, _, direct_libpq = derive("postgres://postgres:p%40ss@supabase-db:5432/postgres")
    env = libpq_env(direct_libpq)
    assert env["PGHOST"] == "supabase-db", env
    assert env["PGPORT"] == "5432", env
    assert env["PGUSER"] == "postgres", env
    assert env["PGDATABASE"] == "postgres", env
    assert env["PGPASSWORD"] == "p@ss", env

    # A pooler input lands on the transaction-mode port, which is why the
    # migrate job no longer needs a port-picking step of its own.
    assert libpq_env(derive(pooler)[2])["PGPORT"] == "6543", pooler

    # A parameter with no libpq environment equivalent is refused, not dropped.
    try:
        libpq_env("postgresql://u:p@h:5432/postgres?sslmode=require")
    except ValueError as err:
        assert "sslmode" in str(err), err
    else:
        raise AssertionError("libpq_env silently dropped an unmapped parameter")

    print("derive-pooler-dsn: self-test ok (59 assertions)")
    return 0


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--dsn")
    ap.add_argument("--host")
    ap.add_argument("--port", default=str(SESSION_PORT))
    ap.add_argument("--user")
    ap.add_argument("--dbname")
    ap.add_argument("--password")
    ap.add_argument(
        "--session-max-conns",
        type=int,
        default=DEFAULT_SESSION_MAX_CONNS,
        help=(
            "this consumer's share of the project's 15 session-mode slots "
            f"(default {DEFAULT_SESSION_MAX_CONNS}, the ephemeral budget; a "
            "long-lived deployment passes a larger one explicitly)"
        ),
    )
    ap.add_argument(
        "--emit-libpq-env",
        action="store_true",
        help=(
            "print PGHOST/PGPORT/PGUSER/PGDATABASE/PGPASSWORD for the "
            "transaction-mode libpq DSN instead of the three DSN lines, for a "
            "caller that has to hand libpq variables to psql"
        ),
    )
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

    try:
        session, transaction, libpq = derive(dsn, session_max_conns=args.session_max_conns)
    except ValueError as err:
        ap.error(str(err))
    if args.emit_libpq_env:
        try:
            env = libpq_env(libpq)
        except ValueError as err:
            ap.error(str(err))
        for name in LIBPQ_ENV_ORDER:
            print(f"{name}={env[name]}")
        # Host, port, user and database on stderr; never the password. Same
        # split as the DSN path below, and for the same reason: a caller
        # redirects stdout into $GITHUB_ENV and still wants a readable log.
        print(
            f"libpq env: PGHOST={env['PGHOST']} PGPORT={env['PGPORT']} "
            f"PGUSER={env['PGUSER']} PGDATABASE={env['PGDATABASE']}",
            file=sys.stderr,
        )
        return 0

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
