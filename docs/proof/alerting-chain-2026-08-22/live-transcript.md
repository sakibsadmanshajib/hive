# Alerting chain, end to end, 2026-08-22

Evidence for the three PR #993 review findings and their fixes. Everything below
is a real command against a real process. No credential, address token or
connection string appears in this file; the SMTP password is passed to
Alertmanager by file reference and never enters any config text quoted here.

## 1. The defects, measured on the live demo box before designing anything

Read only, via `ssh hive-demo-cf`.

Prometheus had not loaded the rule merged that same morning in PR #993:

```
$ curl -s localhost:9090/api/v1/rules
GROUP /etc/prometheus/alerts.yml hive-critical
    HighAPIErrorRate inactive
    UpstreamProviderDown inactive
    PaymentWebhookFailures inactive
GROUP /etc/prometheus/alerts/rate-limit.yml hive_rate_limit
    HighRateLimitRejections inactive
    VerifiedTierUnusuallyRejecting inactive
```

`SignupProvisioningSweepFailing` is absent. Both halves of the defect were live
at once, the inode-pinned mount and the missing reload:

```
--- host alerts.yml first alert:
      - alert: SignupProvisioningSweepFailing
--- container copy first alert:
      - alert: HighAPIErrorRate
--- inode host/guest:
134312
132251
```

Alertmanager's live config, one receiver, one webhook, nothing listening on the
other end since 2026-04-24:

```
route:
  receiver: default
receivers:
- name: default
  webhook_configs:
  - send_resolved: true
    url: <secret>
```

Note `url: <secret>`. Alertmanager redacts webhook URLs in `/api/v2/status`, so
an assertion on the string `localhost:9095` could never fail. An earlier draft of
the deploy check contained exactly that assertion; it was deleted rather than
left in as decoration, and replaced with an assertion on the delivery mechanism.

## 2. The fixed configuration, loaded by the real images

Local rehearsal, `docker compose -p hivealertproof --profile monitoring up -d
prometheus alertmanager` against the branch's own compose file and rule files.

Alertmanager's live config after the change, from `/api/v2/status`:

```
global:
  smtp_from: alerts@scubed.com.bd
  smtp_smarthost: smtp-not-configured.invalid:587
  smtp_auth_password_file: /etc/alertmanager/smtp-credential
  smtp_require_tls: true
route:
  receiver: hive-ops
  group_by:
  - alertname
receivers:
- name: hive-ops
  email_configs:
  - send_resolved: true
    to: contact@scubed.com.bd
    from: alerts@scubed.com.bd
    smarthost: smtp-not-configured.invalid:587
    auth_password_file: /etc/alertmanager/smtp-credential
```

Two things this shows. The receiver is the owner-supplied address, and the
password is a file reference, so it is absent from the config Alertmanager echoes
back and therefore absent from this log and from any deploy output.

All twelve rules load, including the three new signup rules and the new
`alerts/monitoring.yml` group reached through the directory glob:

```
GROUP /etc/prometheus/alerts.yml hive-critical
    SignupProvisioningSweepFailing inactive
    SignupProvisioningFaulting inactive
    SignupProvisioningStrandedIdentity inactive
    HighAPIErrorRate inactive
    UpstreamProviderDown inactive
    PaymentWebhookFailures inactive
GROUP /etc/prometheus/alerts/monitoring.yml hive_monitoring_selfcheck
    AlertDeliveryFailing inactive
    PrometheusConfigReloadFailed inactive
    AlertmanagerDown inactive
GROUP /etc/prometheus/alerts/rate-limit.yml hive_rate_limit
    HighRateLimitRejections inactive
    VerifiedTierUnusuallyRejecting inactive
```

## 3. A rule loads, fires, and arrives at Alertmanager

A throwaway `alerts/zz-e2e-smoke.yml` with `expr: vector(1)` was added and then
deleted in the same session. It is not part of the branch.

Written to disk, and visible inside the container, yet not loaded, which is the
defect in miniature:

```
$ docker exec ... ls /etc/prometheus/alerts/
monitoring.yml
rate-limit.yml
zz-e2e-smoke.yml
$ curl -s localhost:9090/api/v1/rules
AlertingChainSmoke loaded: False
rules: 11
```

After the apply step's SIGHUP:

```
$ docker compose ... kill -s HUP prometheus
$ curl -s localhost:9090/api/v1/rules
rules: 12
   AlertingChainSmoke unknown
$ docker inspect -f '{{.State.Status}} started={{.State.StartedAt}} restarts={{.RestartCount}}'
running started=2026-08-22T19:21:42.235307267Z restarts=0
```

The process reloaded in place. It did not restart, so this is a reload and not a
container replacement wearing a reload's clothes.

Firing:

```
$ curl -s localhost:9090/api/v1/alerts
AlertingChainSmoke firing 2026-08-22T19:22:34.256665104Z
```

Arrived at Alertmanager and routed to the intended receiver:

```
$ curl -s localhost:9093/api/v2/alerts/groups
GROUP labels {'alertname': 'AlertingChainSmoke'} receiver hive-ops
   alert AlertingChainSmoke active receivers ['hive-ops']
```

Delivery attempted against the email integration, and failing honestly, naming
the reserved address:

```
level=WARN msg="Notify attempt failed, will retry later" receiver=hive-ops
  integration=email[0] aggrGroup="{}:{alertname=\"AlertingChainSmoke\"}"
  err="establish connection to server: dial tcp: lookup
  smtp-not-configured.invalid on 127.0.0.11:53: no such host"
level=ERROR msg="Notify for alerts failed"
  err="hive-ops/email[0]: notify retry canceled after 7 attempts: ..."
```

And that failure is measurable rather than only logged, through the new
alertmanager scrape job, which is what `AlertDeliveryFailing` keys on:

```
$ curl -s 'localhost:9090/api/v1/query?query=increase(alertmanager_notifications_failed_total[15m])'
   email 3.1656355583964935
```

That was the state before a relay existed. Section 3a is the same chain against
the real one.

## 3a. The same chain against the real relay, on the box

A relay was supplied and written to the box's `.env` by the owner. Everything in
this section ran on the demo box against that relay. No credential is printed
anywhere: presence and length only, and every line was passed through a redactor
keyed on the login and the password before printing.

```
relay host present: True
relay port: 587 (submission, so STARTTLS rather than implicit TLS)
login present: True, length 24
password present: True, length 90
```

First a direct SMTP client, so each verb's response could be reported verbatim,
which Alertmanager does not expose on the success path. Sender and recipient
exactly as the owner specified:

```
MAIL FROM: no_reply@hive.scubed.co
RCPT TO:   contact@scubed.com.bd
subject: Hive self-hosted SMTP check, 2026-08-22T19:59:52Z, this is a test

EHLO      -> 250
STARTTLS  -> 220 2.0.0 Ready to start TLS
EHLO(tls) -> 250
AUTH      -> 235 2.0.0 Authentication succeeded
MAIL FROM -> 250 2.0.0 Roger, accepting mail from <no_reply@hive.scubed.co>
RCPT TO   -> 250 2.0.0 I'll make sure <contact@scubed.com.bd> gets this
DATA      -> 502 5.7.0 Your SMTP account is not yet activated. Please contact us
             at contact@sendinblue.com to request activation.
```

Three facts from that, none of them inferred:

- **The credentials are correct.** `AUTH -> 235`. STARTTLS on 587 negotiated
  cleanly and `smtp_require_tls` was never turned off.
- **Sender verification did not reject this sender.** `no_reply@hive.scubed.co`
  was accepted at `MAIL FROM` with an explicit 250, and the recipient at
  `RCPT TO`. No substitute sender was tried.
- **The blocker is Brevo account activation, not anything in this repository.**
  The relay accepts the login, the sender and the recipient, then refuses any
  message body until the account is activated. Owner action, on Brevo's side.

Deliberately NOT used: `smtplib`'s debug level. It echoes the `AUTH` command,
whose payload is the base64 of the login and password, and a redactor keyed on the
literal values cannot catch the encoded form. Each verb was issued separately
instead.

Then the same alert through Alertmanager, in a throwaway container on the box
bound to loopback only, with its own name and no compose project, so the live
stack was untouched:

```
container status: running
receiver in the live config: hive-ops with email_configs
--- firing one alert ---
amtool alert add rc=0
level=WARN  msg="Notify attempt failed, will retry later" receiver=hive-ops
  integration=email[0] aggrGroup="{}:{alertname=\"AlertingChainSmtpProof\"}"
  err="delivery failure: 502 \"5.7.0 Your SMTP account is not yet activated.
  Please contact us at contact@sendinblue.com to request activation.\""
level=ERROR msg="Notify for alerts failed"
  err="hive-ops/email[0]: notify retry canceled after 6 attempts: delivery
  failure: 502 \"5.7.0 Your SMTP account is not yet activated. ...\""
```

So the full chain is proven on the box: rule loaded, alert fired, routed to
`hive-ops`, Alertmanager opened a TLS session to the real relay, authenticated,
offered the owner's sender and recipient, and reached the relay's own refusal,
which it reports verbatim and loudly rather than swallowing.

**What is still not proven, and is not claimed: a message reaching the mailbox.**
Nothing has been delivered to `contact@scubed.com.bd`. That is blocked on the
Brevo account activation quoted above, not on configuration.

The container was removed and the working directory deleted by the same script;
verified afterwards that the box has no `amproof` container, no `/tmp/amproof`,
19 containers up, `chat-hive` 200, `console-hive` 307 and `api-hive` 401 without a
key, all as before.

One correction to my own instrumentation, recorded because it is the same class of
mistake this PR is about: the script read
`alertmanager_notifications_failed_total{integration="email"}` by taking the first
matching line, and that counter also carries a `reason` label, so it printed a
zero series while other series for the same integration were non-zero. The alert
rule is unaffected, since `increase(...) > 0` evaluates per series, but a
one-series read of a multi-series counter is exactly how a check ends up unable to
fail.

## 4. Every new check shown failing on purpose

The deploy's verification step, extracted from the workflow text itself rather
than retyped, run in four states.

Against the fixed local stack, passing, with the honest warning:

```
::warning::Alerts are routed and grouped correctly but CANNOT BE DELIVERED: no
SMTP relay is configured, so Alertmanager is pointed at the reserved address
smtp-not-configured.invalid and every send fails. ... To make mail arrive, set
ENTERPRISE_SMTP_HOST, ENTERPRISE_SMTP_PORT, ENTERPRISE_SMTP_USER,
ENTERPRISE_SMTP_PASS and ENTERPRISE_SMTP_ADMIN_EMAIL in the box's .env and
redeploy.
Prometheus is serving the repository configuration, all 12 repository alert
rules are loaded, and Alertmanager routes to receiver 'hive-ops'.
EXIT=0
```

Against the live box's real pre-fix state, read only, failing on both defects at
once:

```
::error::Prometheus has not loaded these alert rules that exist in the
repository: SignupProvisioningSweepFailing. Loaded: HighAPIErrorRate,
HighRateLimitRejections, PaymentWebhookFailures, UpstreamProviderDown,
VerifiedTierUnusuallyRejecting
::error::Alertmanager has no email delivery configured, so every alert it
receives is discarded. ...
EXIT=1
```

With a throwaway extra scrape job added to `prometheus.yml` and no reload:

```
::error::Prometheus is scraping ['alertmanager', 'control-plane', 'edge-api',
'prometheus'] but deploy/prometheus/prometheus.yml declares ['alertmanager',
'control-plane', 'edge-api', 'prometheus', 'temporary-failability-probe']. The
mount is stale or the reload did not take.
EXIT=1
```

That last one is why this section exists. The first version of that assertion
compared the two sets with a regex that matched neither side, because both files
write `- job_name:` as a list item and the pattern required `job_name:` at the
start. Both sets came back empty, they compared equal, and the check reported
green over a genuinely drifted config. It was a check that could not fail, and it
was found only by trying to make it fail.

## 5. The inode-pinned mount, reproduced and repaired

Reproducing what `git pull` does, a write-and-rename that allocates a new inode:

```
host inode before: 2696893
guest inode before: 2696893
host inode after replace: 3569300
guest inode after replace: 2696893
guest sees the change: 0
--- apply loop ---
/etc/prometheus/prometheus.yml: mounted copy already matches ../prometheus/prometheus.yml
/etc/prometheus/alerts.yml: mounted copy differs from ../prometheus/alerts.yml, so the bind mount is pinned to a replaced inode; prometheus must be recreated
 Container hivealertproof-prometheus-1 Recreated
 Container hivealertproof-prometheus-1 Started
guest sees the change after apply: 1
```

The container kept showing the old inode and could not see the change at all, so
a reload alone would have exited zero and applied nothing. The apply step
detected the divergence by content, recreated the service, and the change went
live. The file was then restored the same way and the stack torn down.

## 6. The self-clearing gauge

`go test ./apps/control-plane/internal/signup/... -run TestReconciler` against a
local `pgvector/pgvector:pg17` bootstrapped with `.github/ci/test-db-bootstrap.sql`
plus the full migration chain, which is the same setup ci.yml uses:

```
=== RUN   TestReconcilerFaultRecordSurvivesIdentityAgingOut
--- PASS: TestReconcilerFaultRecordSurvivesIdentityAgingOut (0.17s)
=== RUN   TestReconcilerStrandedCountIgnoresProvisionedIdentities
--- PASS: TestReconcilerStrandedCountIgnoresProvisionedIdentities (0.32s)
```

With `r.recordFault()` removed, which is the code as it stood before this change:

```
--- FAIL: TestReconcilerFaultRecordSurvivesIdentityAgingOut (0.14s)
        Error:      	Not equal:
                    	expected: 1
                    	actual  : 0
FAIL
```

And the collector test, with the counter closure changed to snapshot its value
instead of reading the source on each scrape:

```
--- FAIL: TestRegisterSignupProvisioningExportsBothSeries (0.00s)
        Error:      	Not equal:
                    	expected: int(3)
                    	actual  : float64(0)
```

## 7. The self-check rules have live, moving inputs

Both metrics the new `alerts/monitoring.yml` rules key on were confirmed to exist
and to be scraped, rather than assumed from their names:

```
$ curl -s 'localhost:9090/api/v1/query?query=prometheus_config_last_reload_successful'
   {'__name__': 'prometheus_config_last_reload_successful', 'instance': 'localhost:9090', 'job': 'prometheus'} 1
$ curl -s 'localhost:9090/api/v1/query?query=up{job="alertmanager"}'
up{job=alertmanager} = 1
```

And `PrometheusConfigReloadFailed` was made to fire on purpose, by dropping a
deliberately malformed rule file into `alerts/` and reloading:

```
prometheus_config_last_reload_successful = 0 (0 means PrometheusConfigReloadFailed will fire)
```

then recovered by removing it and reloading again:

```
recovered to 1
Prometheus is serving the repository configuration, all 11 repository alert
rules are loaded, and Alertmanager routes to receiver 'hive-ops'.
```

Worth noting what that sequence also demonstrates: Prometheus kept serving its
previous good configuration and stayed up, which is precisely why this rule is
needed. A rejected rule file is not a crash, it is a silent divergence between
what the repository says and what is being evaluated.
