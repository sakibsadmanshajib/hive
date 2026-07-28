-- Issue #536: role selection at invite time.
--
-- account_invitations.role was pinned to 'member' by its CHECK constraint, so a
-- workspace could never invite a co-owner: the console had no role selector and
-- the database would have rejected one anyway. Widen the constraint to the same
-- role set account_memberships already allows ('owner', 'member'), which is what
-- accounts.NormalizeRole enforces in the control-plane.
--
-- Idempotent: drops the old constraint if present, then adds the widened one
-- under a stable name.

alter table public.account_invitations
  drop constraint if exists account_invitations_role_check;

alter table public.account_invitations
  add constraint account_invitations_role_check
  check (role in ('owner', 'member'));
