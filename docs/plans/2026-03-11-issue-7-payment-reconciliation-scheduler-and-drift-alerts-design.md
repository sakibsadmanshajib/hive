# Issue #7 Payment Reconciliation Scheduler And Drift Alerts Design

## Goal

Add an automated payment reconciliation capability that detects billing drift, emits actionable alerts for operators, and fits the current API-centric runtime without introducing unnecessary infrastructure.

## Context

- The repository already has manual payment reconciliation and credit-ledger audit guidance in `docs/runbooks/active/payments-reconciliation.md` and `docs/runbooks/active/credit-ledger-audit.md`.
- Persistent billing state already exists in Supabase-backed tables for `payment_intents`, `payment_events`, `credit_accounts`, and `credit_ledger`.
- The API runtime currently has no generic scheduler or cron framework.
- Issue `#7` is labeled billing hardening and financial risk reduction, so the first implementation should minimize financial blind spots with low blast radius.

## Decision

Implement a reusable reconciliation service inside the API domain/runtime, then wrap it with an opt-in in-process scheduler.

The reconciliation service remains callable directly so it can be tested deterministically and reused later by a CLI command, admin endpoint, or external worker if operations evolve beyond a single API instance.

## Rejected Alternatives

### Operator-run command only

This is the smallest possible change, but it does not adequately satisfy the scheduler requirement and still depends on humans remembering to run it.

### Separate worker or cron service

This is a stronger long-term architecture, but it adds infrastructure and deployment surface the current repository does not otherwise use for billing jobs.

## Architecture

### Reconciliation core

Add a reconciliation service that reads recent payment intents, payment events, and credited ledger evidence from the billing store and classifies drift conditions.

The service should produce:

- a job summary with counts by drift class
- per-intent findings with enough identifiers for operator follow-up
- a no-drift result when the scan completes cleanly without mismatches

### Scheduler wrapper

Add a small runtime scheduler that:

- is disabled by default
- runs on a fixed interval from environment configuration
- skips overlapping runs inside the same process
- logs failures and drift findings in structured form

The scheduler should depend on the reconciliation service rather than embedding billing queries directly.

## Drift Rules

First implementation should detect these classes:

1. Verified payment event exists, but the payment intent is not marked `credited`.
2. Payment intent is `credited`, but there is no verified payment event for that intent.
3. Payment intent is `credited`, but `minted_credits` does not equal `bdt_amount * 100`.
4. Payment intent is `credited` or has a verified payment event, but there is no corresponding payment-ledger evidence for that intent (`missing_payment_ledger_entry`).

These rules map directly to the existing manual runbook and current billing conversion logic.

## Scope Boundaries

In scope:

- automated detection
- scheduler enablement and safeguards
- operator-visible alerts through logs
- docs and tests

Out of scope for this issue:

- auto-remediation or automatic credit adjustments
- Slack/email/pager integrations
- new web UI for drift status
- multi-instance distributed scheduling coordination

## Runtime Safeguards

- Reconciliation uses a configurable lookback window so it scans recent relevant data rather than all historical records on every interval.
- Scheduler runs only when explicit env flags enable it.
- A process-local in-flight guard prevents overlapping runs in one API instance.
- If billing persistence is unavailable or misconfigured, the scheduler should no-op cleanly or fail fast during startup configuration, depending on how the existing runtime handles required billing dependencies.

## Testing Strategy

- Add unit tests for reconciliation classification logic with mocked billing-store reads.
- Add runtime tests for scheduler enablement and overlap prevention.
- Keep time-based behavior testable by injecting a clock and scheduler callback boundaries rather than relying on real delays.

## Operational Impact

- Operators gain automatic log-based drift alerts.
- Manual reconciliation remains the resolution path; the runbook should explain how the automated scan complements the existing export-and-investigate workflow.
- New env vars must be documented clearly because the feature is opt-in and operationally sensitive.

## Verification

- Targeted API tests for reconciliation and scheduler wiring
- Full API test suite
- API build

If implementation remains API-only, no web verification is required for this issue.
