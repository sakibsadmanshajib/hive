# Issue 553 merge-gate probe

Throwaway file used to verify the rebuilt CI merge gate. Case: documentation
only. Expected result: the `changes` job reports `run=false` and every required
check is skipped rather than left pending, so the pull request is mergeable
without running the full suite.

This branch is deleted after the evidence is captured.
