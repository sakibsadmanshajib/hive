# Issue 553 merge-gate probe

Throwaway file used to verify the rebuilt CI merge gate. Case: code plus
documentation in one commit, which is the shape that used to fire both the real
workflow and the no-op workflow at once. Expected result: the `changes` job
reports `run=true` and the real suite decides the outcome.

This branch is deleted after the evidence is captured.
