# Live capture: rate limiting configured, counted, refused and displayed (issue #1725, PR #1733)

Captured 2026-09-02 against the branch `feat/1725-real-visible-rate-limits`, on
an isolated stack built from that branch. Not against the demo box, and
deliberately not: the box runs `main`, where the migration in this PR has not
been applied, and applying it there before merge would make every existing
`rolling_five_hour_limit` NULL while `main`'s `scanRatePolicy` still scans that
column into a non-nullable int64, which would fail every authorization resolve
on the live gateway. The capture that belongs on the deployed box is owed after
this merges and deploys.

## Substrate

Every component below is this branch's own code, freshly built from the
worktree:

* `pgvector/pgvector:pg17`, a throwaway database, schema applied by the real
  applier: `scripts/ci-throwaway-db.sh` reported `throwaway database ready: 133
  of 133 migrations executed`, the last of them
  `20260902_02_usage_window_limits.sql` from this PR.
* `redis:7-alpine`, empty at the start of each run below.
* `hive-control-plane:ci` and `hive-edge-api:ci`, both rebuilt from this branch
  (`docker compose build control-plane edge-api`). The first run of this capture
  used a stale `hive-edge-api:ci` left by an earlier session and printed the old
  refusal text, which is how the staleness was caught; every transcript below is
  from the rebuilt images.
* `hive-web-console-prod`, built from this branch.

One component is a stand-in, named here rather than left to be discovered: the
identity provider. A twelve line Python server answers the one route
control-plane calls to resolve a viewer, `GET /auth/v1/user`, so the capture
needs no GoTrue instance and no account password is set, reset or rotated
anywhere (`docs/live-test-auth.md`). Nothing else is faked. In particular the
usage figures on the console screenshots are read by control-plane out of the
same Redis keys edge-api's limiter wrote during this same run.

The fixture account, its key and its database are destroyed with the network at
the end of the run and exist nowhere else.

## 1. The writer exists, and refuses a zero

`PUT /api/v1/accounts/current/rate-limits`, platform admin only, against the
real database. This is the writer those two columns have never had.

```
--- GET (before) ---
{"account_id":"22222222-...","session_configured":true,"session_limit":1000000000,
 "weekly_anchor_at":"2026-08-31T17:54:08Z","weekly_configured":true,"weekly_limit":4000000000}

--- PUT zero (must be refused) ---
422

--- PUT weekly only (session must survive) ---
{"session_configured":true,"session_limit":1000000000,"weekly_configured":true,"weekly_limit":5000000000}

--- PUT session null (clears) ---
{"session_configured":false,"session_limit":null,"weekly_configured":true,"weekly_limit":5000000000}

--- db ---
 rolling_five_hour_limit | weekly_limit
-------------------------+--------------
                         |   5000000000
```

An omitted field leaves its limit alone, an explicit null clears it, and a zero
is refused with 422 rather than silently stored as "unlimited". The database
column is NULL for the cleared window, not 0, which the new CHECK constraint
would have rejected anyway.

## 2. The limiter counts, in the shared key space

Eleven requests were sent to `POST /v1/chat/completions` with the fixture key,
against a session allowance of 1,000,000,000 and a weekly allowance of
4,000,000,000. Each request reserved 100,000,000.

```
rlwin:{acct:22222222-2222-2222-2222-222222222222}:session:5961240 = 300000000
rlwin:{acct:22222222-2222-2222-2222-222222222222}:weekly:0        = 300000000
```

The weekly bucket index is 0 because the account's anchor is two days old and
the anchored window is one seven day bucket, so the counter key itself changes
when the week rolls. The session bucket index is epoch aligned five minute
bucketing.

## 3. The refusal names its window and its reset

Verbatim, after the session allowance was spent:

```
HTTP/1.1 429 Too Many Requests
Ratelimit-Limit: 100
Ratelimit-Policy: "session";q=100;w=18000
Ratelimit-Remaining: 0
Ratelimit-Reset: 17861
Retry-After: 17561
X-Ratelimit-Session-Remaining-Percent: 0
X-Ratelimit-Session-Reset: 17861
X-Ratelimit-Session-Reset-At: 2026-09-02T23:10:00Z
X-Ratelimit-Session-Used-Percent: 100

{"error":{"message":"You have used all of your session allowance. Hive measures a session
 over a rolling five hour window. It resets at 2026-09-02T23:05:00Z (in 4 hours).",
 "type":"rate_limit_error","param":null,"code":"session_limit_exceeded",
 "reset_at":"2026-09-02T23:05:00Z","limit_window":"session"}}
```

`Retry-After` (17561) is earlier than the full drain (17861) on purpose: the
first is when enough score ages out of the sliding window for this request to
fit, the second is when the window empties completely. Before this PR the
limiter reported neither, only "milliseconds until the current bucket rolls",
and the message was "Rate limit reached for your current quota window. Please
try again later."

No currency figure and no absolute allowance appears anywhere in the response.
The windows ship as percentages (D-068, D-070).

## 4. Two defects this capture found, both fixed in the PR

* A first request against a completely unused window refused with "You have
  used all of your session allowance" and a header set reading `0` percent
  used, because the request's own estimate was larger than the whole allowance.
  Fixed: the message now says how much is actually left when the window is not
  exhausted. Guarded by `TestRefusalDistinguishesOversizedFromExhausted`.
* The same refusal filled `x-ratelimit-reset-requests`, which names the requests
  per minute window, with a reset four hours out. Fixed: that header now ships
  only when a per minute request limit is what refused. Guarded by
  `TestLongWindowRefusalDoesNotForgeAPerMinuteHeader`.

## 5. The customer surface

`GET /api/v1/accounts/current/usage-windows`, read live by the console:

```
{"account_id":"22222222-...","windows":[
 {"window":"session","configured":true,"used_percent":30,"resets_at":"2026-09-02T23:00:00Z",
  "window_seconds":18000,"anchored":false},
 {"window":"weekly","configured":true,"used_percent":7,"resets_at":"2026-09-07T17:54:08Z",
  "window_seconds":604800,"anchored":true}],
 "read_at":"2026-09-02T18:02:58Z"}
```

The weekly reset is the account's anchor plus seven days to the second
(anchor 2026-08-31T17:54:08Z), which is the anchored behaviour the owner ruled
for and not the rolling one the code had.

Screenshots posted to PR #1733:

* `console-usage-windows.png`: session 30 percent used with "Resets Sep 2, 2026,
  11:10 PM", weekly 7 percent used with "Restores in full at your weekly reset.
  Resets Sep 7, 2026, 5:54 PM".
* `console-limit-reached.png`: the same card after the allowance was spent,
  session at 100 percent with a "Limit reached" badge, weekly at 25 percent.

The refusal text itself is in section 3 rather than in an image, because the
chat front end is not part of this PR: forwarding these headers through Open
WebUI's error path and drawing the same two bars in chat are the two pieces
left open on the issue.

## Credentials

No flow in this capture carries a credential in a URL. The fixture API key is
not reproduced here; the account, the key and the database it lived in were
destroyed with the docker network at the end of the run.
