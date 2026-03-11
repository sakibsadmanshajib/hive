# CI and Architectural Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement PostgreSQL RPCs to eliminate TOCTOU races, fix GitGuardian/ESLint CI failures, and add missing test coverage.

**Architecture:** We are shifting critical state transitions for payments and credits into Supabase PostgreSQL RPCs to ensure ACID compliance without needing distributed locks.

**Tech Stack:** TypeScript, Node.js, Fastify, Supabase (PostgreSQL), Vitest

---

### Task 1: Fix CI Failures

**Files:**
- Modify: `.env.example`
- Modify: `apps/api/src/runtime/services.ts`

**Step 1: Write minimal implementation**
- In `.env.example`, change `LANGFUSE_PUBLIC_KEY` and `LANGFUSE_SECRET_KEY` to `your-langfuse-public-key-here` and `your-langfuse-secret-key-here`.
- In `services.ts`, remove or prefix `hashPassword` and `verifyPassword` imports from `./security` with an underscore if unused.

**Step 2: Run test to verify it passes**
Run: `pnpm --filter @hive/api lint`
Expected: PASS (no warnings)

**Step 3: Commit**
```bash
git add .env.example apps/api/src/runtime/services.ts
git commit -m "fix: resolve gitguardian and eslint ci failures"
```

---

### Task 2: Supabase RPC Migrations

**Files:**
- Create/Modify: `supabase/migrations/20260223000004_billing_rpcs.sql`

**Step 1: Write minimal implementation**
Create a new migration file with:
```sql
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
```

**Step 2: Run test to verify it passes**
Run: `npx supabase db reset`
Expected: Migration applies successfully

**Step 3: Commit**
```bash
git add supabase/migrations/
git commit -m "feat: add atomic billing rpcs for payment claim and consumption"
```

---

### Task 3: Refactor TypeScript Services for RPCs & Revocation

**Files:**
- Modify: `apps/api/src/runtime/services.ts`
- Modify: `apps/api/src/runtime/supabase-api-key-store.ts`
- Modify: `apps/api/src/runtime/supabase-billing-store.ts`

**Step 1: Write the failing test**
Update `test/domain/payment-service.test.ts` to expect the atomic claim flow.

**Step 2: Run test to verify it fails**
Run: `pnpm --filter @hive/api test payment-service`
Expected: FAIL due to missing RPC implementation bindings mapping.

**Step 3: Write minimal implementation**
- `services.ts`: Update `applyWebhook` to call `this.store.claimPaymentIntent()`.
- `supabase-billing-store.ts`: Implement `claimPaymentIntent` using `supabase.rpc('claim_payment_intent', {...})`.
- `supabase-billing-store.ts`: Implement `consumeCredits` using `supabase.rpc('consume_credits', {...})`.
- `supabase-api-key-store.ts`: Refactor `revoke` to use a single `.update({revoked: true, revoked_at: ...}).eq('key_hash', hash).eq('revoked', false).select()`.

**Step 4: Run test to verify it passes**
Run: `pnpm --filter @hive/api test payment-service` and `pnpm --filter @hive/api test credits-ledger`
Expected: PASS

**Step 5: Commit**
```bash
git add apps/api/src/runtime/
git commit -m "refactor: migrate payment claims and consumption to atomic rpcs"
```

---

### Task 4: Missing Test Coverage

**Files:**
- Create: `apps/api/test/domain/persistent-usage-service.test.ts`
- Create: `apps/api/test/domain/persistent-user-service.test.ts`
- Create: `apps/api/test/domain/runtime-services.test.ts`

**Step 1: Write the failing test**
Create the test files with `.me()`, `.validateApiKey()`, `.revokeApiKey()`, `.add()`, `.list()`, and `createRuntimeServices()` behaviors.

**Step 2: Run test to verify it fails**
Run: `pnpm --filter @hive/api test persistent-use persistent-usage runtime-services`
Expected: FAIL if mocks aren't wired up, but the implementation already exists so they might just pass once written properly.

**Step 3: Write minimal implementation**
No implementation changes needed, just tests.

**Step 4: Run test to verify it passes**
Run: `pnpm --filter @hive/api test`
Expected: All 64+ tests pass.

**Step 5: Commit**
```bash
git add apps/api/test/domain/
git commit -m "test: add coverage for usage, user, and runtime services"
```
