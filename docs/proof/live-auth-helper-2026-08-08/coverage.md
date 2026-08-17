# Agent-workspace coverage gate, re-run with the live-session helper

Surface: the agent workspace (Cowork, `apps/agent-console`, served at
`/agent-workspace` on `chat-hive.scubed.co`). Gate and control ledger come from
PR #799; the only change for this run was replacing its password-form `signIn()`
with `reauthenticate()` from `apps/web-console/tests/e2e/support/live-auth.ts`.

| run | proven | blocked on credentials |
| --- | --- | --- |
| PR #799 as filed (2026-08-08, no credentials) | 8 / 22 | 14 |
| with the live-session helper | **21 / 22** | 0 |

```
15 passed, 1 skipped (1.2m)
agent-workspace coverage: 21/22
  UNPROVEN C8: Sign-in: submit button (valid credentials path) --
    HIVE_QA_AGENT_PASSWORD not set. Every other authenticated control is proven
    from a minted session (docs/live-test-auth.md); this one is the password
    submit path itself and cannot be. Rotating the shared demo password to
    fabricate one is forbidden.
```

C8 is the honest remainder, not a gap the helper can close: it is the sign-in
button's own valid-credentials path, so proving it needs a real password typed
into the real form. Supplying an existing password is fine; inventing one by
rotating the shared account is what is forbidden. C3/C4 already prove the same
button's invalid-credentials path, and C8 is proven the moment
`HIVE_QA_AGENT_PASSWORD` is provided for an account that is OWNER on a
cowork-enabled tenant.

`signed-in-workspace.png` in this directory is the same storage state driving a
real browser: `/agent-workspace/tasks` loaded and signed in, composer and task
list rendered. The URL overlay carries no credential, because the session lives
in a cookie rather than a fragment.

## Two findings from the run, neither a product defect

1. **C10/C11/C12 needed a locator fix in PR #799's spec, not a product fix.**
   `getByRole("radio", { name: "Knowledge work" }).check()` times out with
   "label intercepts pointer events": the `<input>` is `peer sr-only` and the
   visible control is the `<span>` inside its `<label>`
   (`apps/agent-console/components/task-console.tsx`). Clicking the label is
   both what makes the assertion pass and what a real user does. Reported on
   PR #799.

2. **Two mints for the same account that interleave race.** GoTrue keeps one
   outstanding one-time token per user, so the earlier mint's `verify` answers
   `403 Email link is invalid or has expired`. Observed once during this pass.
   `mintSession` now retries once on exactly that message. The real ceiling is
   documented in the code: give each parallel worker its own account rather
   than minting for one account from several workers at once.

## Environment note

Midway through this pass `chat-hive.scubed.co` stopped answering entirely
(`net::ERR_TIMED_OUT` on plain `page.goto`, and `curl` timing out at 25s on
both `/` and the sign-in path), for roughly three minutes, then recovered with
no intervention. Unauthenticated controls failed alongside authenticated ones
during that window, which is how it was told apart from a credential problem.
