begin;

create or replace function public.handle_new_user()
returns trigger as $$
begin
  insert into public.user_profiles (user_id, gateway_user_id, email, name)
  values (
    new.id,
    'usr_' || substr(md5(random()::text), 1, 10),
    new.email,
    new.raw_user_meta_data->>'full_name'
  );
  return new;
end;
$$ language plpgsql security definer set search_path = public;

drop trigger if exists on_auth_user_created on auth.users;
create trigger on_auth_user_created
  after insert on auth.users
  for each row execute procedure public.handle_new_user();

-- Backfill any existing users that missed the trigger
insert into public.user_profiles (user_id, gateway_user_id, email, name)
select
  id,
  'usr_' || substr(md5(random()::text), 1, 10),
  email,
  raw_user_meta_data->>'full_name'
from auth.users
where id not in (select user_id from public.user_profiles);

commit;
