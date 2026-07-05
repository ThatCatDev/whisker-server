# whisker-server

Sync and boards backend for [Whisker](../whisker). One Go binary, three jobs:

- **`/sync/{board}`** — Yjs websocket relay. The server is deliberately
  *dumb*: it never materializes CRDT documents, it stores updates as opaque
  blobs and shuttles bytes between peers. Memory per room is the connection
  set plus cached presence payloads — no per-document heap.
- **`/api/boards`** — board registry REST API (list/create/rename/delete,
  owner + members ACL).
- **Auth** — verifies Supabase-issued JWTs locally (HS256, the project's JWT
  secret). Supabase is used for *auth only*; no other Supabase feature is
  required.

## Run it

Development (no database, no auth):

```sh
AUTH_DISABLED=1 go run .
```

With Postgres and real auth:

```sh
docker compose up -d postgres
DATABASE_URL=postgres://whisker:whisker@localhost:5432/whisker \
SUPABASE_JWT_SECRET=<Project Settings → API → JWT Secret> \
go run .
```

The schema (`internal/store/schema.sql`) is applied automatically on boot.
`DATABASE_URL` can point at the Postgres bundled with your Supabase project.

## Configuration

| Variable              | Default | Meaning                                        |
| --------------------- | ------- | ---------------------------------------------- |
| `ADDR`                | `:8787` | Listen address                                 |
| `DATABASE_URL`        | —       | Postgres DSN; empty = in-memory dev store      |
| `SUPABASE_JWT_SECRET` | —       | HS256 secret for verifying Supabase JWTs       |
| `AUTH_DISABLED`       | —       | `1` skips auth (dev only, implies OPEN_BOARDS) |
| `OPEN_BOARDS`         | —       | `1` skips the board ACL on `/sync` (dev only)  |
| `CORS_ORIGIN`         | `*`     | `Access-Control-Allow-Origin` for the REST API |

## Client hookup

The Whisker client attaches a `y-websocket` provider when a sync URL is set
(see Whisker's README). Auth travels as `?token=<supabase access token>` on
the websocket URL and `Authorization: Bearer` on REST calls.

## How sync works (and why there's no CRDT library here)

Answering "what updates is this client missing?" is the only part of the Yjs
protocol that needs CRDT semantics. This server sidesteps it:

1. Client connects and sends sync-step-1. The server ignores the state
   vector and replies with **every stored blob** (redundant updates are
   idempotent in Yjs), followed by its own sync-step-1 carrying an **empty
   state vector**.
2. The client answers with sync-step-2 containing its **entire document
   state** — which, because websocket messages are processed in order,
   provably includes every blob from step 1 plus the client's own offline
   edits.
3. That answer becomes the board's new single snapshot (when the log has
   grown past a threshold), replacing everything it subsumes. Storage is
   self-compacting as a side effect of clients connecting.
4. Subsequent edits flow as incremental updates: appended, broadcast.

Presence (cursors) is relayed the same way, with the last payload per
connection cached for newcomers and an explicit "left" broadcast on
disconnect.

Trade-offs of the dumb relay, chosen deliberately:

- A client with write access could replace a board's content with garbage in
  its snapshot answer — the same trust you extend to any editor of a shared
  document, just worth naming.
- Sync sends the full stored state rather than a minimal diff. Whisker
  boards are small (shapes, not rich text); simplicity wins.
- Single-instance by design. For multiple instances you'd add a pub/sub
  fan-out (e.g. Postgres LISTEN/NOTIFY or NATS) between relays.

## Deploying

`Dockerfile` builds a ~10 MB scratch image. Put it behind TLS (the client
uses `wss://`), point `DATABASE_URL` at managed Postgres, set
`SUPABASE_JWT_SECRET`, and leave `AUTH_DISABLED`/`OPEN_BOARDS` unset.
