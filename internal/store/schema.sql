-- Whisker board metadata + Yjs document update log.
-- Applied idempotently on server start.

create table if not exists boards (
  id         text primary key,
  owner_id   text not null,
  name       text not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists boards_owner on boards (owner_id);

create table if not exists board_members (
  board_id text not null references boards (id) on delete cascade,
  user_id  text not null,
  role     text not null default 'editor',
  primary key (board_id, user_id)
);

create index if not exists board_members_user on board_members (user_id);

-- Opaque Yjs update blobs, replayed in seq order on client sync. The relay
-- compacts them into a single snapshot whenever a freshly-synced client
-- reports back its merged state.
create sequence if not exists board_updates_seq;

create table if not exists board_updates (
  board_id text   not null,
  seq      bigint not null default nextval('board_updates_seq'),
  data     bytea  not null,
  primary key (board_id, seq)
);
