begin;

create or replace function public.refund_credits(
  p_user_id uuid,
  p_credits integer,
  p_reference_id text
) returns boolean as $$
begin
  if p_credits is null or p_credits <= 0 or p_credits > 1000000 then
    raise exception 'invalid credits amount';
  end if;

  if p_reference_id is null or length(trim(p_reference_id)) = 0 or length(p_reference_id) > 128 then
    raise exception 'invalid reference_id';
  end if;

  update public.credit_accounts
  set available_credits = available_credits + p_credits,
      updated_at = now()
  where user_id = p_user_id;

  if not found then
    return false;
  end if;

  insert into public.credit_ledger (user_id, entry_type, credits, reference_type, reference_id)
  values (p_user_id, 'credit', p_credits, 'refund', p_reference_id);

  return true;
end;
$$ language plpgsql security definer set search_path = public;

revoke execute on function public.refund_credits(uuid, integer, text) from public;
grant execute on function public.refund_credits(uuid, integer, text) to service_role;

commit;
