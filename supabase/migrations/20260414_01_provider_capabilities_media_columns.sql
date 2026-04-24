alter table public.provider_capabilities
  add column if not exists supports_image_generation boolean not null default false,
  add column if not exists supports_image_edit boolean not null default false,
  add column if not exists supports_tts boolean not null default false,
  add column if not exists supports_stt boolean not null default false,
  add column if not exists supports_batch boolean not null default false;

update public.provider_capabilities
set
  supports_image_generation = true,
  supports_image_edit = true,
  supports_tts = true,
  supports_stt = true,
  supports_batch = true
where route_id = 'route-openrouter-auto';
