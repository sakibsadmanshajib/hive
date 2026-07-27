# Issue 553 harness case: code plus documentation

Throwaway file. The pull request that carries it also changes a Go source file,
which is the exact shape that used to fire the real workflow and the no-op
workflow at the same time. The verbatim `changes` job must report `run=true` and
the required-shaped job must execute its real step.
