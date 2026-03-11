begin;

create or replace function public.claim_payment_intent(
  p_intent_id text,
  p_provider text,
  p_provider_txn_id text
) returns jsonb as $$
declare
  v_intent public.payment_intents;
  v_minted integer;
  v_inserted_event_key text;
begin
  if p_intent_id is null or length(trim(p_intent_id)) = 0 or length(p_intent_id) > 128 then
    return jsonb_build_object('success', false, 'error', 'invalid intent_id');
  end if;

  if p_provider not in ('bkash', 'sslcommerz') then
    return jsonb_build_object('success', false, 'error', 'invalid provider');
  end if;

  if p_provider_txn_id is null or length(trim(p_provider_txn_id)) = 0 or length(p_provider_txn_id) > 256 then
    return jsonb_build_object('success', false, 'error', 'invalid provider_txn_id');
  end if;

  -- lock the intent
  select * into v_intent
  from public.payment_intents
  where intent_id = p_intent_id and provider = p_provider
  for update;

  if not found then
    return jsonb_build_object('success', false, 'error', 'intent not found or provider mismatch');
  end if;

  if v_intent.status = 'credited' then
    return jsonb_build_object(
      'success', true,
      'intent', jsonb_build_object(
        'intent_id', v_intent.intent_id,
        'user_id', v_intent.user_id,
        'provider', v_intent.provider,
        'bdt_amount', v_intent.bdt_amount,
        'status', v_intent.status,
        'minted_credits', v_intent.minted_credits
      )
    );
  end if;

  v_minted := trunc(v_intent.bdt_amount * 100);

  insert into public.payment_events (event_key, intent_id, provider, provider_txn_id, verified)
  values (p_provider || ':' || p_provider_txn_id, p_intent_id, p_provider, p_provider_txn_id, true)
  on conflict do nothing
  returning event_key into v_inserted_event_key;

  if v_inserted_event_key is null then
    return jsonb_build_object('success', false, 'error', 'duplicate callback');
  end if;

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
  return jsonb_build_object(
    'success', true,
    'intent', jsonb_build_object(
      'intent_id', v_intent.intent_id,
      'user_id', v_intent.user_id,
      'provider', v_intent.provider,
      'bdt_amount', v_intent.bdt_amount,
      'status', v_intent.status,
      'minted_credits', v_intent.minted_credits
    )
  );
end;
$$ language plpgsql security definer set search_path = public;

create or replace function public.consume_credits(
  p_user_id uuid,
  p_credits integer,
  p_reference_id text
) returns boolean as $$
declare
  v_available integer;
begin
  if p_credits is null or p_credits <= 0 or p_credits > 1000000 then
    raise exception 'invalid credits amount';
  end if;

  if p_reference_id is null or length(trim(p_reference_id)) = 0 or length(p_reference_id) > 128 then
    raise exception 'invalid reference_id';
  end if;

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
$$ language plpgsql security definer set search_path = public;

revoke execute on function public.claim_payment_intent(text, text, text) from public;
grant execute on function public.claim_payment_intent(text, text, text) to service_role;

revoke execute on function public.consume_credits(uuid, integer, text) from public;
grant execute on function public.consume_credits(uuid, integer, text) to service_role;

commit;
