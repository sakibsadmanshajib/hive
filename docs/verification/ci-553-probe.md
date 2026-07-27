# Issue 553 merge-gate probe

Throwaway file used to verify the rebuilt CI merge gate. Case: code plus
documentation in one commit, where the code deliberately fails. Expected result:
the `changes` job reports `run=true`, the real Go suite fails, and no other job
can report success under the same required check name.

This branch is deleted after the evidence is captured.
