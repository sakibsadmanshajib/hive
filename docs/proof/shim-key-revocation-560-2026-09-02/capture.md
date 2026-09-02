# Issue #560 proof: the seeder cannot revoke the deployment's shim key, and a dead key now pages

Captured 2026-09-02 against the live demo box (`ssh hive-demo`, repository at
`~/hive`, stack running under compose project `hive`). No live credential was
changed by any command below. The key is identified by the sha256 of the value
in the box's own `.env`, and only its last six characters are printed.

## 1. Live state of the shim key before anything ran

The headline question in the issue: is the box's `OWUI_SHIM_KEY` alive right
now. It is, and it is not on the account CI rotates.

```
$ raw=$(grep -E '^SUPABASE_DB_URL=' .env | head -1 | cut -d= -f2-)
$ eval "$(python3 scripts/derive-pooler-dsn.py --dsn "$raw" --emit-libpq-env | sed 's/^/export /')"
libpq env: PGHOST=supabase-db PGPORT=5432 PGUSER=postgres PGDATABASE=postgres
$ shim=$(grep -E '^OWUI_SHIM_KEY=' .env | head -1 | cut -d= -f2-)
$ h=$(printf %s "$shim" | sha256sum | cut -d' ' -f1)
$ scripts/stack-psql.sh -tA -F'|' -c "select a.slug, k.nickname, k.status, k.created_at,
    coalesce(k.revoked_at::text, 'NOT REVOKED') from public.api_keys k
    join public.accounts a on a.id = k.account_id where k.token_hash = '$h'" </dev/null

hive-demo-owui-shim|hive-demo-owui-shim-key|active|2026-07-26 13:07:28.349815+00|NOT REVOKED
```

That is the correct invocation, and it is the one the issue asks to be
recorded: libpq variables derived from the box's own `SUPABASE_DB_URL` with
`scripts/derive-pooler-dsn.py --emit-libpq-env`, then `scripts/stack-psql.sh`
run from the repository root on the box (the self-hosted data plane publishes no
host port, so a host-side `psql` cannot resolve `supabase-db`). Redirect stdin,
because that wrapper consumes it.

The running gateway agrees:

```
$ docker logs hive-edge-api-1 2>&1 | grep -i 'owui:' | tail -1
2026/09/02 16:11:39 owui: OWUI_SHIM_KEY resolves to an active Hive API key on a
tenant-provisioned account; Open WebUI model listing, document RAG embeddings,
and text-to-speech can authenticate
```

## 2. The seeder now refuses the invocation that could revoke it

Run on the box from a scratch directory, with this branch's copy of the script.
The shared checkout was not modified.

```
### the invocation that used to be able to revoke it: a deployment account, nothing to update
$ python3 /tmp/shimproof/seed-owui-e2e-user.py --account-slug hive-demo-owui-shim --tenant-slug hive-demo
error: refusing to rotate hive-demo-owui-shim, a deployment account, without
updating anything that carries the key. Revoking here and syncing nothing is
exactly the outage in issue #560. Pass --env-file <path to that deployment's
compose .env>, and set OWUI_BASE_URL with OWUI_ADMIN_TOKEN so Open WebUI's
persisted config is updated too.
exit=2

### CI shape still allowed: the guard passes, and it then dies on an unreachable database having touched nothing
$ SUPABASE_URL=http://127.0.0.1:9 SUPABASE_SERVICE_ROLE_KEY=not-a-real-key \
    python3 /tmp/shimproof/seed-owui-e2e-user.py --account-slug owui-e2e-shim
    raise URLError(err)
urllib.error.URLError: <urlopen error [Errno 111] Connection refused>
```

The second run is the shape the nightly uses, and it is deliberately still
allowed: the guard passes it through and the script then fails on the
unreachable database, having minted and revoked nothing.

## 3. Live state after, unchanged

```
hive-demo-owui-shim|hive-demo-owui-shim-key|active|2026-07-26 13:07:28.349815+00|NOT REVOKED
```

The account boundary, visible:

```
$ scripts/stack-psql.sh -tA -F'|' -c "select a.slug, k.nickname, k.status, count(*)
    from public.api_keys k join public.accounts a on a.id = k.account_id
    where a.slug in ('owui-e2e-shim','hive-demo-owui-shim') group by 1,2,3 order by 1" </dev/null

hive-demo-owui-shim|hive-717-diagnostic-2026-08-04-delete-me|revoked|1
hive-demo-owui-shim|hive-demo-owui-shim-key|active|1
owui-e2e-shim|owui-e2e-shim-key|active|1
```

Note the nickname on the live key: `hive-demo-owui-shim-key`, not the
`owui-e2e-shim-key` the first revision of this script wrote. It was minted by
hand, which is why the third layer matters: the cleanup filters on a nickname,
so a key minted by another route is never revoked by a rotation that knows
nothing about who carries it.

Security review then pointed out that a fixed nickname made that protection a
coincidence rather than a property, and one with a short life: the documented
remedy mints unconditionally, so the first recovery run would have renamed the
box's key to the constant and destroyed the very thing sparing it. The nickname
is now derived from the account slug (`key_nickname`), which reproduces
`hive-demo-owui-shim-key` for `hive-demo-owui-shim` exactly as the row above
shows, so recovery preserves the name already on the box, CI's cleanup and a
deployment's can never name the same key, and the remedy run revokes the key it
just replaced instead of leaving a superseded credential active forever. A key
under any other nickname is still left alone, and `.env.example` and the alert
now both tell the operator to revoke that one deliberately.

## 4. The loud signal fires

The gauge, through a real `Gather` on the metrics registry an actual `/metrics`
scrape reads (`go test ./apps/edge-api/cmd/server/... -run OWUIShimKey`):

```
--- PASS: TestOWUIShimKeyGaugeGoesToZeroOnADeadKey (0.00s)
--- PASS: TestOWUIShimKeyGaugeHoldsThroughATransientProbeFailure (0.01s)
--- PASS: TestOWUIShimKeyGaugeIsAbsentWithoutAShimKey (0.00s)
--- PASS: TestOWUIShimKeyGaugeIsReadByAnAlertRule (0.00s)
```

The alert rule, evaluated by Prometheus itself rather than asserted by
inspection. Input series: usable for two minutes, then zero.

```
$ docker run --rm -v $PWD:/rules -w /rules --entrypoint promtool prom/prometheus:latest check rules alerts.yml
Checking alerts.yml
  SUCCESS: 9 rules found

$ docker run --rm -v $PWD:/rules -w /rules --entrypoint promtool prom/prometheus:latest test rules owui-shim-key_test.yml
  SUCCESS
```

The test asserts both halves: nothing firing at 5 minutes, and at 9 minutes
exactly one `OWUIShimKeyUnusable` alert with `severity=critical` and the full
summary and description text. Mutating the expectation at 9 minutes to "no
alerts" turns it red, which was run rather than assumed:

```
  FAILED:
    alertname: OWUIShimKeyUnusable, time: 9m,
        exp:[],
        got:[
            0:
              Labels:{alertname="OWUIShimKeyUnusable", severity="critical"}
              Annotations:{description="edge-api cannot resolve the Open WebUI shim key ...
```

## 5. How a human finds out

The delivery chain is already standing on the box and was checked, not assumed:

```
$ docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'prometheus|alertmanager|grafana'
hive-prometheus-1     Up 8 days
hive-alertmanager-1   Up 9 days
hive-grafana-1        Up 8 days

$ docker exec hive-prometheus-1 wget -qO- 'http://localhost:9090/api/v1/targets?state=active'
alertmanager up
control-plane up
edge-api up          <- the job that scrapes the new gauge, healthy, no scrape error
prometheus up

$ docker exec hive-prometheus-1 wget -qO- 'http://localhost:9090/api/v1/rules'
hive-critical 8      <- the group this alert is added to
hive_monitoring_selfcheck 3
hive_rate_limit 2
```

`deploy/docker/docker-compose.yml` routes every `hive-critical` alert to the
`hive-ops` receiver, whose `email_configs` sends to
`ENTERPRISE_SMTP_ADMIN_EMAIL` over the relay this stack already authenticates
against. So a revoked key becomes an email to the ops mailbox within about ten
minutes (one five-minute probe interval plus the rule's six-minute `for`),
rather than a customer noticing that document upload stopped answering.

## 6. Rebased onto PR #1712 after it merged mid-flight

PR #1712 merged while this branch was in review, which is what made the branch
conflict: it edits the same comment block in `apps/edge-api/cmd/server/main.go`.
Resolved in its favour, and the alert's own wording was corrected to match what
is now true. After #1696, Open WebUI's document RAG embeddings carry the
signed-in user's own token, so a dead shim key no longer breaks them; what it
still breaks is text-to-speech and speech-to-text, both of which are
`OWUI_SHIM_KEY` (`AUDIO_TTS_OPENAI_API_KEY` and `AUDIO_STT_OPENAI_API_KEY`), plus
the bodyless `GET /v1/models`. The `promtool test rules` case above was re-run
against the corrected annotations and passes unchanged; the summary now reads
`OWUI_SHIM_KEY does not resolve; chat text-to-speech and speech-to-text are
down`, and `.env.example`'s list of what the key authenticates was corrected in
the same pass, since it had gone stale the moment #1712 landed.

## 7. Review round two (2026-09-02, security review 5093605279)

Five threads, all applied. What was re-run afterwards, on this dev box rather
than the demo box: no live credential was read, changed or revoked in this
round, and the live rows above are the same rows, unmodified.

The rules, including the two new ones, evaluated by Prometheus itself:

```
$ docker run --rm -v $PWD:/rules -w /rules --entrypoint promtool \
    prom/prometheus:latest check rules alerts.yml monitoring.yml
Checking alerts.yml
  SUCCESS: 10 rules found
Checking monitoring.yml
  SUCCESS: 4 rules found

$ docker run --rm -v $PWD:/rules -w /rules --entrypoint promtool \
    prom/prometheus:latest test rules owui-shim-key_test.yml
  SUCCESS
```

Four cases, and the last two are the ones that were missing before:

* usable for two minutes then zero, with a verdict on every probe: nothing at
  5m, exactly one `OWUIShimKeyUnusable` at 9m with `severity=critical` and the
  full corrected annotation text, and no `OWUIShimKeyVerdictStale`.
* a fresh verdict on every probe out to 40m: `OWUIShimKeyVerdictStale` silent.
  The discriminating negative, since at 9m that rule cannot fire whatever the
  series holds.
* the blind spot: `hive_owui_shim_key_usable` sitting at the 1 it is registered
  with while `hive_owui_shim_key_last_verdict_seconds` stays 0, so nothing has
  ever been measured. `OWUIShimKeyUnusable` cannot fire on that, and
  `OWUIShimKeyVerdictStale` does, at `severity=warning`.
* the scrape stops entirely (`up{job="edge-api"}` goes to 0): every rule reading
  an edge-api series goes quiet, because an expression over a series that no
  longer exists matches nothing, and `EdgeAPITargetDown` fires. An absent scrape
  can no longer read as healthy.

The seeder guards, each confirmed red against a mutation of the code rather
than assumed (`python3 scripts/test_seed_owui_e2e_user.py`, 34 checks):

```
key_nickname reverted to the constant   -> red (nickname derivation + call sites)
casefold comparison removed             -> red (a cased reserved slug was allowed a consumer)
empty-slug refusal removed              -> red (an empty account slug was allowed)
env-file gate removed from the delete   -> red (the deletion is no longer gated on the env-file check)
```

Go: `go vet ./apps/edge-api/cmd/...` clean, and
`go test ./apps/edge-api/cmd/... -run OWUIShimKey` passes, including the new
`TestOWUIShimKeyVerdictTimestampOnlyMovesOnARealVerdict` (the timestamp series
does not move on transient probe failures, and is 0 before the first verdict so
an age rule fires on "never measured") and the extended
`TestOWUIShimKeyGaugeIsReadByAnAlertRule`, which now also fails if either series
loses its rule or if nothing alerts on `up{job="edge-api"}`.
