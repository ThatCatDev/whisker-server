-- Runs once on first Postgres boot (docker-entrypoint-initdb.d).
-- Supabase Auth (GoTrue) keeps its tables in a dedicated schema and
-- migrates itself on start; it needs the schema to exist, plus a role
-- named "postgres" that its migrations grant to (a Supabase convention).
create schema if not exists auth;
create role postgres superuser login password 'postgres';
