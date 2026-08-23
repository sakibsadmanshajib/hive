# Proof: an admin in one tenant stops reading another tenant's Knowledge

Fixes hive#947. This is a security claim, so what has to be shown is the
**negative** case, against a control that rules out the two ways such a claim
usually lies: that the test account was never an admin, and that the "fix"
simply broke the feature for everyone.

Supplements the 2026-08-18 capture already on this PR with a re-run on the
current stack. This branch is rebased onto `main` at `82375d07f`, where #952
and #956 have already landed, and the capture stack additionally carries #951,
which merges ahead of this one. So the substrate is the state that will
actually exist when this PR lands, and `main` at that point also carries
#1043's LiteLLM digest pin and #1012's actual-cost billing. The migration
chain was brought current against the local database first.

Both arms were re-run from scratch after the rebase. The after arm reproduced
byte for byte; the before arm differs only in its timestamps.

Text log only here; images are posted as permanent release assets through
`scripts/post-pr-visual-proof.sh`. `npm run lint:proof-tokens` scans this
directory and nothing else.

## Method

One local stack. Same two accounts, same Knowledge collection, same
containers, same database volume across both arms. The **only** thing that
changes between them is the two environment variables this PR sets, applied by
recreating the `open-webui` container and confirmed live with
`docker inspect` before the second arm ran.

- **before** — the flags at their upstream default, `true`. This is not a
  simulation of the bug: it is what every deployment runs today, because these
  variables were never set, and upstream defaults both to `true` in
  `config.py`. On a branch that has already fixed them, forcing them back
  explicitly is the only way to reproduce that state, so the before arm adds a
  scratch compose overlay that sets exactly those two variables and nothing
  else.
- **after** — the flags as this PR sets them, `false`, straight from the
  branch's own `docker-compose.yml` with no overlay.

The values in force were read back from the running container with
`docker inspect` at the start of each arm, so which arm was which is a
recorded fact rather than an intention.

Two independent tenants, each provisioned through `scripts/seed-demo-owner.py`
with its own tenant and account slug:

- `owner@hive-verify-952.invalid`, tenant A, owns the collection
  `Verify-960-Tenant-A-Secret-Kb`
- `owner-b@hive-verify-960.invalid`, tenant B, no relationship to tenant A or
  its collection

Both signed in through a real OAuth 2.1 authorization-code round trip. Both
accounts' Open WebUI role was read from the running server with
`GET /api/v1/auths/` **at the moment of each capture**, so "B is an admin" is
observed rather than asserted in prose.

## Result

| | before (flags `true`) | after (flags `false`) |
|---|---|---|
| Tenant A role | `admin` | `admin` |
| Tenant B role | `admin` | `admin` |
| Distinct accounts | yes | yes |
| B's `GET /api/v1/knowledge/` | HTTP 200, **1 entry** | HTTP 200, **0 entries** |
| A's collection in B's API response | **true** | **false** |
| A's collection on B's Knowledge page | **true** | **false** |
| A still sees its own collection | true | **true** |

The last row is the control that matters most. If the flags had merely broken
Knowledge, tenant A would have lost sight of its own collection too. A still
sees it after the fix, so what changed is cross-tenant reach and not the
feature.

In the before screenshot the leak is visible with attribution: signed in as
tenant B, the page reads "Knowledge 1" and lists
`Verify-960-Tenant-A-...` with the byline "By Owner@hive-verify-952.invalid",
an account in a different tenant. After, the same page for the same account
reads "Knowledge 0" and "No knowledge found".

## A failure this capture nearly recorded as a pass

The first attempt at the after arm ran before the recreated `open-webui`
container was healthy. Every request answered 401, so tenant B "saw nothing"
and the arm would have read as a clean pass while proving only that nobody was
signed in.

The capture script now aborts outright when either account's role is not
`admin` at capture time, rather than warning and continuing. A signed-out arm
is indistinguishable from a working fix by the metric under test, so it must
never be allowed to produce a result at all.

## Scope

This closes the read path only. It does not change who holds the Open WebUI
admin role (#748) and it does not address the proxy write-verb gap (#949).
`/v1/rag/chat` is a separate store with its own tenant scoping in edge-api's
own Go code and is unaffected either way.

## Credential handling

No credential appears in any captured URL for this flow, and none was needed:
sessions came from the normal OAuth round trip, not from a token in a query
string or fragment. The redaction list is applied to the log and to the
screenshot URL banners regardless. Both fixture accounts live only on a local
database created for this run and destroyed with it. No password was set,
read, reset or rotated on any shared or deployed account.
