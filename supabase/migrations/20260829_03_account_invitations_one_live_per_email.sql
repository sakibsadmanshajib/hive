-- Issue #1440: one live invitation per address, enforced by the database.
--
-- Re-inviting somebody is how a workspace re-sends an invitation, so it has to
-- supersede the outstanding one rather than stack a second live token beside
-- it. Two live tokens for one address means revoking either one leaves the
-- other working, which is a revocation that does not revoke.
--
-- Doing that in application code needs a read, a delete and an insert, and it
-- loses either way round. Sweep before the insert and a failed insert leaves the
-- address with no invitation at all, so the link already sitting in somebody's
-- inbox stops working and nothing replaces it. Sweep after the insert and two
-- concurrent invitations for one address delete each other's rows, leaving both
-- callers holding a link that resolves to nothing. There is no ordering that is
-- correct without a lock, and the constraint is cheaper than the lock.
--
-- So: a partial unique index, and CreateInvitation upserts onto it. One
-- statement, no window, and the invariant holds against concurrency the
-- application never sees.
--
-- lower(email) rather than email, because AcceptInvitation compares the invited
-- address with strings.EqualFold. Without the fold, "Sam@example.com" stays
-- redeemable after "sam@example.com" is re-invited.
--
-- accepted_at IS NULL scopes it to live invitations only. An accepted
-- invitation is history and several of them for one address are expected: it is
-- the record of somebody joining, leaving and being invited back.

-- Existing rows can already violate this, because until now nothing stopped
-- them. Keep the newest live invitation per address and drop the rest, which is
-- the same outcome re-inviting was always meant to produce.
delete from public.account_invitations a
using public.account_invitations b
where a.accepted_at is null
  and b.accepted_at is null
  and a.account_id = b.account_id
  and lower(a.email) = lower(b.email)
  and (a.created_at, a.id) < (b.created_at, b.id);

create unique index if not exists account_invitations_one_live_per_email
  on public.account_invitations (account_id, lower(email))
  where accepted_at is null;

-- account_id leads the index, so this also serves the members page's listing of
-- outstanding invitations, which had no usable index at all and scanned the
-- whole table on every load.
