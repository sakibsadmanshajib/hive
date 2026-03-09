begin;

create or replace function public.claim_payment_intent(
  p_intent_id text,
  p_provider text,
  p_provider_txn_id text
) returns jsonb as $$
declare
  v_intent public.payment_intents;
  v_minted integer;
begin
  -- lock the intent
  select * into v_intent
  from public.payment_intents
  where intent_id = p_intent_id and provider = p_provider
  for update;

  if not found then
    return jsonb_build_object('success', false, 'error', 'intent not found or provider mismatch');
  end if;

  if v_intent.status = 'credited' then
    return jsonb_build_object('success', true, 'intent', row_to_json(v_intent));
  end if;

  v_minted := trunc(v_intent.bdt_amount * 100);

  insert into public.payment_events (event_key, intent_id, provider, provider_txn_id, verified)
  values (p_provider || ':' || p_provider_txn_id, p_intent_id, p_provider, p_provider_txn_id, true)
  on conflict do nothing;

  update public.payment_intents
  set status = 'credited', minted_credits = v_minted
  where intent_id = p_intent_id;

  update public.credit_accounts
  set purchased_credits = purchased_credits + v_minted,
      available_credits = available_credits + v_minted,
      updated_at = now()
  where user_id = v_intent.user_id;

  insert into public.credit_ledger (user_id, entry_type, credits, reference_type, reference_id)
  values (v_intent.user_id, 'credit', v_minted, 'payment', p_intent_id);

  select * into v_intent from public.payment_intents where intent_id = p_intent_id;
  return jsonb_build_object('success', true, 'intent', row_to_json(v_intent));
end;
$$ language plpgsql security definer;

create or replace function public.consume_credits(
  p_user_id uuid,
  p_credits integer,
  p_reference_id text
) returns boolean as $$
declare
  v_available integer;
begin
  select available_credits into v_available
  from public.credit_accounts
  where user_id = p_user_id
  for update;

  if v_available is null or v_available < p_credits then
    return false;
  end if;

  update public.credit_accounts
  set available_credits = available_credits - p_credits,
      updated_at = now()
  where user_id = p_user_id;

  insert into public.credit_ledger (user_id, entry_type, credits, reference_type, reference_id)
  values (p_user_id, 'debit', p_credits, 'usage', p_reference_id);

  return true;
end;
$$ language plpgsql security definer;

commit;
