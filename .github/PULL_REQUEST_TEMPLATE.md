## Summary

<!-- One or two sentences: what does this PR do? -->

## Why

<!-- Issue reference. Every PR references an issue; no orphan PRs. -->

Issue #\_\_\_

## What changed

- 

## Test evidence

<!-- Commands you ran and their results. Required for money-path and auth/tenancy changes. -->

```text
$ <command>
<result>
```

## Risk checklist

- [ ] Money-path (billing / credits / payments) touched?
- [ ] Auth / tenancy touched?
- [ ] Migrations included and reversible?
- [ ] Provider names leaked into customer-bound strings?

## Rollback note

<!-- How to revert safely: single commit revert, migration down, feature flag off, etc. -->
