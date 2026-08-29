# Demo box backup and restore runbook

Closes the durability gap recorded in issue #1000: since the move off managed
Supabase Cloud, every production store on the demo box was single-copy. This
runbook documents the mechanism that now backs all four stores up on a
schedule, encrypts them at rest, copies one set off the box, proves restorability
into a throwaway target, and fails loudly when a run did not happen.

## What is backed up

| Store | How it is captured | Why this method |
| --- | --- | --- |
| Postgres (`postgres` database: identities in GoTrue's `auth` schema, tenants, api keys, credit ledger) | `pg_dump --format=custom`, streamed out of `hive-supabase-db-1` via `docker exec` stdout | Consistent snapshot of a running server, no stop or restart, custom format restores with `pg_restore` into any empty database |
| Open WebUI relational state (`hive_owui-data` volume, `/data/webui.db`) | SQLite online backup API run inside the open-webui container, integrity-checked before leaving the container | Copying a SQLite file while its WAL is active is not safe; the backup API produces a consistent copy from a running writer |
| Open WebUI uploads (`/data/uploads`) | `tar` streamed via `docker exec` | Plain files |
| Supabase Storage object bytes (buckets `hive-files`, `hive-images`) | `tar` of `/var/lib/storage` streamed via `docker exec` | Object bytes live on the storage container's volume under a nested `stub/stub/` layout; metadata stays in Postgres and is covered by the DB dump |

Measured sizes on 2026-08-23: db dump about 2.5 MB (custom format), webui.db
about 1 MB, uploads under 100 KB, storage objects about 300 KB. A full daily
set is under 5 MB.

## Where artifacts live

```
/home/sakib/hive-backups/          # on the box, outside any git checkout
├── bin/backup-box.sh              # installed copy of scripts/backup-box.sh
├── etc/passphrase                 # chmod 600, openssl-compatible passphrase
├── etc/backup.env                 # optional overrides for the units
├── daily/YYYY-MM-DD/
│   ├── db.pgdump.enc              # aes-256-cbc, pbkdf2 600000 iterations
│   ├── webui.db.enc
│   ├── uploads.tgz.enc
│   ├── storage.tgz.enc
│   ├── SHA256SUMS                 # over the four .enc artifacts
│   └── MANIFEST.txt
├── status/last-success-epoch      # unix epoch of last good run
├── status/STATUS.txt              # one line per outcome, newest last
└── logs/<ts>.log                  # full run log per invocation
```

Off-box copy: `/home/sakib/hive-backups/hive-demo/daily/...` on the dev
machine, pulled encrypted only (scripts/pull-box-backups.sh). The passphrase
also exists off-box at /home/sakib/hive-backups/etc/passphrase-hive-demo so a
dead box does not take its own key down with it.

That pull is manual, and its staleness is now observable (issue #1491). After a
pull whose checksums verify, and only then, `scripts/pull-box-backups.sh`
records the repository variable `LAST_OFFBOX_BACKUP_PULL` on
`sakibsadmanshajib/hive`, holding the pull time and the newest day copied. A
pull that fails verification leaves the previous value untouched and exits
non-zero, so the marker never claims a copy that did not happen.
`deploy-drift-watchdog.yml`'s `offbox-backup-staleness` job reads that variable
every 30 minutes and fails, opening a tracking issue, once it is older than 72
hours or missing entirely. Absent is treated as stale, never as unknown.

Why a repository variable rather than a file on the box: nothing that runs on a
schedule can read a file on the box. Hosted runners have no SSH path to it, and
the two workflows that do run on its self-hosted runner are triggered by deploys
and labels rather than by a clock, while a check added to this script would be
inert until someone hand-copied the new version into
`/home/sakib/hive-backups/bin`. Only encrypted artifacts still cross the
network; the marker is a timestamp and a date and involves no credential.

Use a dedicated token for that write, not the ambient `gh auth login`. The dev
machine holds the only off-box copy of the production database; before the
marker existed its compromise yielded encrypted backups, and an ambient
`repo`-scoped login in reach of the same machine turns that into repository
write as well. Mint a fine-grained PAT scoped to `sakibsadmanshajib/hive` with
`Variables: read and write` and nothing else, and pass it as `GH_TOKEN` for this
one script.

The marker is an honesty marker, not proof. Anyone who can write repository
variables can set it to a recent timestamp with no pull behind it, and the check
would then report green indefinitely. No `GITHUB_TOKEN` can do that (there is no
Actions permission scope for variables at all, and no workflow in this
repository writes one), so the actor would be an insider or whoever compromised
the dev machine's token, which is the same threat the paragraph above is about.
Every direction that is not a claim of freshness fails closed: absent, empty,
unparseable, offset-less, duplicated and future-dated markers are all stale.

## Schedule, retention, capacity

- systemd USER timer `hive-box-backup.timer`: 03:15 and 15:15 UTC daily,
  Persistent=true catches up after downtime; user-level because there is no
  sudo on the box, reboot-surviving because Linger is enabled for sakib.
- Watchdog cron entry runs `backup-box.sh --check` hourly. It is silent when
  fresh, posts HiveBoxBackupStale when the last success is older than 26h.
  It exists so that the death of the timer itself still surfaces within an hour.
- Retention: 14 daily sets on the box. At measured size that is under 70 MB,
  against roughly 34 GB free on the box root filesystem. Off-box accumulation
  is about 150 MB/month if never pruned; prune by deleting whole day
  directories.

Numbers are chosen against measured reality, not guessed: dump 2.5 MB plus the
other three artifacts keeps 14 days below the size of a single demo chat image.

## Failure signal

The script posts directly to Alertmanager's v2 API on the published host port
(localhost:9093), reusing the existing routing tree and hive-ops email receiver
(PR #998 made that chain work). No new component is deployed:

- `HiveBoxBackupFailed`: posted immediately when any step errors. Auto-resolves
  through resolve_timeout once a later run succeeds (success posts nothing).
- `HiveBoxBackupStale`: posted by any `--check` invocation when the last
  success exceeds 26 hours. Catches missed runs and dead schedulers.

The off-box half has its own, separate signal, because it runs somewhere else
and had none at all until issue #1491: `deploy-drift-watchdog.yml`'s
`offbox-backup-staleness` job. It fails and opens a GitHub issue when the last
verified pull is more than 72 hours old, and the failure text names
`scripts/pull-box-backups.sh` as the fix.

The 72-hour threshold is deliberately looser than the on-box 26 hours. The
writing machine is a laptop that is off for stretches, and a Friday-evening to
Monday-morning gap is about 63 hours, so an ordinary weekend has to stay silent
or the alarm gets muted and the gap goes unobserved again. It stays well inside
the box's own 14-day retention, so when it does fire every missing day is still
recoverable from the box, and it is far tighter than the six days that actually
elapsed unnoticed on 2026-08-29.

That threshold governs both halves of the marker. `pulled_at` says how recently
somebody ran the pull; `newest_day` says how recent the data that pull copied
actually is. They come apart with nobody at fault: a box that stops publishing
new daily sets leaves every later pull transferring nothing, verifying the days
already present, and refreshing `pulled_at`, so a check reading only `pulled_at`
would report green over a copy weeks behind. `newest_day` is aged from the end
of that UTC day, since the value has date granularity and the box's second daily
run lands at 15:15 UTC.

Two things a responder should know before chasing the wrong half:

- A stale `newest_day` under a fresh `pulled_at` means the pull is working and
  the box has published nothing new. Read `~/hive-backups/status/STATUS.txt` and
  `systemctl --user list-timers hive-box-backup.timer` on the box first.
- The pull verifies every day it holds, not only the newest, so one corrupted
  old local day directory (bitrot on a ten-day-old artifact) fails the whole run
  and leaves the marker unchanged, and the alarm then stays red even though
  today's pull was perfect. That is deliberate. Delete the bad day directory
  locally and pull again; the box still holds it.

At-a-glance checks, newest line tells the story:

```
cat /home/sakib/hive-backups/status/STATUS.txt
systemctl --user list-timers hive-box-backup.timer
journalctl --user -u hive-box-backup -n 50
```

## Installing or reinstalling on the box

Run as sakib on the box, from a checkout containing these files:

```
mkdir -p ~/hive-backups/bin ~/hive-backups/etc
cp scripts/backup-box.sh ~/hive-backups/bin/backup-box.sh
cp scripts/restore-box-backup.sh ~/hive-backups/bin/restore-box-backup.sh
chmod +x ~/hive-backups/bin/*.sh
[ -f ~/hive-backups/etc/passphrase ] || { umask 077; openssl rand -base64 48 > ~/hive-backups/etc/passphrase; }
chmod 600 ~/hive-backups/etc/passphrase ~/hive-backups/etc/backup.env
mkdir -p ~/.config/systemd/user
cp deploy/systemd-user/hive-box-backup.{service,timer} ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now hive-box-backup.timer
loginctl show-user "$USER" -p Linger   # must print Linger=yes
(crontab -l 2>/dev/null; echo '17 * * * * /home/sakib/hive-backups/bin/backup-box.sh --check >> /home/sakib/hive-backups/logs/check.log 2>&1') | crontab -
~/hive-backups/bin/backup-box.sh       # first run, watch it work
```

## Restore proof (safe, automated, no production contact)

One command on the box:

```
/home/sakib/hive-backups/bin/restore-box-backup.sh
```

Decrypts today's db artifact to a private temp dir, stands up a throwaway
Postgres from the same image production uses (`hive-supabase-db:pg16-cron`,
network-isolated with `--network none`), restores, compares row counts for the
ledger, tenant, api-key and identity tables plus storage metadata against the
live database read-only, prints a verdict, destroys the throwaway. Extension
DDL errors during restore are expected (pg_cron needs shared-preload config the
vanilla entrypoint does not set); data row counts decide the verdict.

Verified live 2026-08-23: all six compared tables matched (counts in PR body).

Quick webui and storage verification commands (manual):

```
openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -salt -pass file:~/hive-backups/etc/passphrase \
  -in ~/hive-backups/daily/<day>/webui.db.enc -out /tmp/webui-check.db
python3 -c "import sqlite3;print(sqlite3.connect('/tmp/webui-check.db').execute('PRAGMA integrity_check').fetchone()[0])"
docker exec hive-open-webui-1 python3 -c "import sqlite3;print(sqlite3.connect('file:/data/webui.db?mode=ro',uri=True).execute('select count(*) from chat').fetchone()[0])"
python3 -c "import sqlite3;print(sqlite3.connect('/tmp/webui-check.db').execute('select count(*) from chat').fetchone()[0])"

openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -salt -pass file:~/hive-backups/etc/passphrase \
  -in ~/hive-backups/daily/<day>/storage.tgz.enc -out /tmp/storage-check.tgz
tar tzf /tmp/storage-check.tgz | wc -l               # entry count vs live:
docker exec hive-supabase-storage-1 find /var/lib/storage | wc -l
```

Table names in the webui examples (chat, user, knowledge) are current Open WebUI schema and can drift across upstream upgrades; if a count query errors, list tables with `.tables` first. Always shred decrypted temps afterwards:

```
shred -u /tmp/webui-check.db /tmp/storage-check.tgz
```

## Real restore onto production (deliberately manual)

No script automates writing over production. A human decides, then follows
these steps. Assume the stack is stopped or the affected service recreated by
whoever declares the disaster; this section covers data, not orchestration.

### Database

```
# decrypt
openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -salt -pass file:~/hive-backups/etc/passphrase \
  -in ~/hive-backups/daily/<day>/db.pgdump.enc -out /tmp/db.pgdump
# restore into the existing postgres database. Connect to template1 for both
# administrative commands: Postgres refuses to drop the database the session
# is connected to. drop/create is destructive by design, do it consciously,
# and only once every client of this database is stopped.
docker exec hive-supabase-db-1 psql -U postgres -d template1 -c 'DROP DATABASE postgres;'
docker exec hive-supabase-db-1 psql -U postgres -d template1 -c 'CREATE DATABASE postgres;'
docker exec -i hive-supabase-db-1 pg_restore -U postgres -d postgres --no-owner < /tmp/db.pgdump || true
shred -u /tmp/db.pgdump
```

The trailing `|| true` swallows extension DDL failures exactly as the verify
script documents; check the ledger and identity counts afterwards with the
same comparison the verify script prints.

### Open WebUI state

The restore REPLACES webui.db, it never merges. Open WebUI keeps the database open and its WAL files beside it, so a stale webui.db-wal replaying over a restored file would corrupt it: the -wal and -shm files must go with the old database, and every swap happens while the container is stopped.

```
# 1. decrypt
openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -salt -pass file:~/hive-backups/etc/passphrase \
  -in ~/hive-backups/daily/<day>/webui.db.enc -out /tmp/webui.db
# 2. copy the restored file into the volume under a staging name
docker cp /tmp/webui.db hive-open-webui-1:/data/webui.db.restored
# 3. stop the writer (coordinate with whoever owns the incident)
docker stop hive-open-webui-1
# 4. swap files inside the volume using a throwaway helper on an image the box
#    already has (no new pull), since a stopped container cannot exec
docker run --rm -v hive_owui-data:/data --entrypoint sh hive-supabase-db:pg16-cron -c '
  mv /data/webui.db /data/webui.db.pre-restore
  rm -f /data/webui.db-wal /data/webui.db-shm
  mv /data/webui.db.restored /data/webui.db'
# 5. back up
docker start hive-open-webui-1
shred -u /tmp/webui.db
```

Keep webui.db.pre-restore until chat history is confirmed intact.

Open WebUI holds webui.db open, so replacing the live file requires restarting
the open-webui container, which is out of scope for an automated path here.
Coordinate the restart with whoever owns the incident, then move the restored
file into place across the restart.

### Storage objects

The restore REPLACES the object tree, it never merges. Extracting over the live tree would preserve objects created after the backup day while the restored Postgres metadata no longer knows about them, so the bucket bytes and the restored storage.objects rows must agree. Coordinate stopping the storage container with whoever owns the incident, replace the whole tree, then start again.

```
# 1. decrypt
openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -salt -pass file:~/hive-backups/etc/passphrase \
  -in ~/hive-backups/daily/<day>/storage.tgz.enc -out /tmp/storage.tgz
# 2. stop the writer (coordinate with whoever owns the incident)
docker stop hive-supabase-storage-1
# 3. replace the whole tree through a throwaway helper mounting the volume
#    plus host /tmp read-only for the archive
docker run --rm \
  -v hive_supabase-storage-data:/storage \
  -v /tmp:/host-tmp:ro \
  --entrypoint bash hive-supabase-db:pg16-cron -c '
  set -e
  mkdir -p /storage/.pre-restore
  find /storage -mindepth 1 -maxdepth 1 ! -name .pre-restore -exec mv {} /storage/.pre-restore/ +
  tar xzf /host-tmp/storage.tgz --strip-components=2 -C /storage'
shred -u /tmp/storage.tgz
```

The backup tar was created as `tar czf - -C / var/lib/storage`, so its entries carry a `var/lib/storage/` prefix that `--strip-components=2` removes; the volume root becomes `/storage` inside the helper. The old tree is moved into `/storage/.pre-restore` inside the same volume: verify the extracted content, then delete `.pre-restore` (through a second helper run) and `docker start hive-supabase-storage-1`.

## Gaps this does not close

- True offsite destination: one encrypted copy lives on the dev machine, which
  stops the box-death scenario but not dev-machine-plus-box co-loss. Choosing
  the real offsite/object-store destination is an owner decision, tracked in
  #1492. The staleness of whatever copy exists is now visible (#1491), which is
  a different property: the alarm tells you the copy is behind, it does not
  make the copy durable.
- Scheduling the pull itself: deliberately not done. That machine is a laptop
  and is off for long stretches, so a timer there would recreate an
  unobservable half with a false sense of coverage, which is the failure this
  is meant to end rather than repeat. Coverage equals however often someone
  runs the pull, and the point of the marker is that everyone can see it.
- Point-in-time recovery: pg_dump gives snapshots at run times, nothing finer.
- Encryption is aes-256-cbc with a per-file salt and PBKDF2 (600k iterations);
  checksums detect corruption but are not signed, so an attacker who can write
  inside /home/sakib/hive-backups could tamper with artifacts without
  detection. Adequate for this box's threat model; if the threat model grows,
  move to age or encrypt-then-HMAC before trusting these files in anger.
- Prometheus-scraped metric: would need node_exporter, which this deployment
  does not run. The direct Alertmanager post reuses the transport that already
  works; revisit if node_exporter lands later. Related hardening shipped with
  this change: alertmanager now binds host port 9093 to loopback only, since
  the backup failure channel made its unauthenticated API load-bearing.
