# AGENTS.md

Rules of engagement for coding agents working in this repository. Human
contributors: see [CONTRIBUTING.md](CONTRIBUTING.md) and
[CLAUDE.md](CLAUDE.md).

## Read order (every session)

1. `CLAUDE.md` first: build, test, and project conventions.
2. `.wolf/cerebrum.md` before generating any code (do-not-repeat list).
3. `.wolf/anatomy.md` before reading unfamiliar areas (hook-maintained map).
4. `.wolf/decisions.md` before any design, spec, plan, or implementation.

## Worktree discipline

- The shared checkout is sacred. Create your own worktree:
  `git worktree add .claude/worktrees/<name> -b <branch> origin/main`, and
  work only there.
- Never run checkout, reset, or clean against the shared checkout.
- Never force-push shared branches. Push with
  `git push origin HEAD:<branch>` and verify with `git ls-remote`.
- `.wolf/` telemetry files are hook-owned and gitignored: never hand-edit or
  commit them.

## Issues first

- No pull request without an issue reference.
- Tracking epic [#1010](https://github.com/sakibsadmanshajib/hive/issues/1010)
  defines the gate priority order; do not jump gates.

## Review pipeline

- Run an adversarial self-review of your own diff before pushing.
- Expect domain review on every PR; auth, money, and input-parsing paths get
  security review.
- Merge policy: all required checks green, every review thread resolved,
  squash merge, branch deleted after merge.

## Money path and auth

- Changes touching billing, credits, payments, auth, or tenancy require
  explicit test evidence in the PR body: commands run plus results.
- Provider names never leak into customer-bound strings; sanitize at both the
  control-plane and edge boundaries.
- All financial math uses `math/big`; floats never touch money paths.

## Buglog protocol

- Fixed bugs are logged as one JSON line per entry in `.wolf/buglog.jsonl`,
  appended only on `main` via a dedicated buglog-only PR after your fix has
  merged (one such PR open at a time).
- While the fix is in flight, carry the entry in the fix PR body under a
  "Buglog entry" heading. Never commit to buglog or telemetry files on a
  feature branch.

## Secrets

- Configuration via environment variables only. Validate required vars at
  startup.
- Never log secrets, never commit `.env`, never paste keys into issues,
  PRs, or screenshots.
