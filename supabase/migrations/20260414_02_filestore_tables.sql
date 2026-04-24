create table if not exists public.files (
    id text primary key,
    account_id text not null,
    purpose text not null,
    filename text not null,
    bytes bigint not null,
    status text not null default 'uploaded',
    storage_path text not null,
    created_at timestamptz not null default now(),
    expires_at timestamptz
);

create index if not exists idx_files_account_id
    on public.files(account_id);

create table if not exists public.uploads (
    id text primary key,
    account_id text not null,
    filename text not null,
    bytes bigint not null,
    mime_type text not null,
    purpose text not null,
    status text not null default 'pending',
    s3_upload_id text,
    storage_path text not null,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null
);

create index if not exists idx_uploads_account_id
    on public.uploads(account_id);

create table if not exists public.upload_parts (
    id text primary key,
    upload_id text not null references public.uploads(id),
    part_num int not null,
    etag text not null,
    created_at timestamptz not null default now(),
    unique (upload_id, part_num)
);

create index if not exists idx_upload_parts_upload_id
    on public.upload_parts(upload_id);

create table if not exists public.batches (
    id text primary key,
    account_id text not null,
    input_file_id text not null references public.files(id),
    output_file_id text references public.files(id),
    error_file_id text references public.files(id),
    endpoint text not null,
    completion_window text not null default '24h',
    status text not null default 'validating',
    provider text not null default '',
    upstream_batch_id text,
    reservation_id text,
    request_counts_total int not null default 0,
    request_counts_completed int not null default 0,
    request_counts_failed int not null default 0,
    created_at timestamptz not null default now(),
    in_progress_at timestamptz,
    completed_at timestamptz,
    failed_at timestamptz,
    cancelled_at timestamptz,
    expires_at timestamptz not null default (now() + interval '24 hours')
);

create index if not exists idx_batches_account_id
    on public.batches(account_id);
