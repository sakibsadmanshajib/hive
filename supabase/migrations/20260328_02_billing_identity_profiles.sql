-- Phase 2: durable billing identity hooks used later by checkout and invoicing.

create table public.account_billing_profiles (
  account_id                    uuid        primary key references public.accounts(id) on delete cascade,
  billing_contact_name          text,
  billing_contact_email         text,
  legal_entity_name             text,
  legal_entity_type             text        not null default 'individual' check (
    legal_entity_type in (
      'individual',
      'sole_proprietor',
      'private_company',
      'public_company',
      'non_profit'
    )
  ),
  business_registration_number  text,
  vat_number                    text,
  tax_id_type                   text,
  tax_id_value                  text,
  country_code                  text,
  state_region                  text,
  created_at                    timestamptz not null default now(),
  updated_at                    timestamptz not null default now()
);
