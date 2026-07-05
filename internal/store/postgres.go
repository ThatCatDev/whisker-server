package store

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// Postgres stores board metadata and update blobs in PostgreSQL. Works
// against any Postgres, including the one bundled with a Supabase project.
type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) ListBoards(ctx context.Context, userID string) ([]Board, error) {
	rows, err := p.pool.Query(ctx, `
		select id, owner_id, name, created_at, updated_at from boards
		where owner_id = $1
		   or exists (select 1 from board_members m where m.board_id = boards.id and m.user_id = $1)
		order by updated_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Board{}
	for rows.Next() {
		var b Board
		if err := rows.Scan(&b.ID, &b.OwnerID, &b.Name, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateBoard(ctx context.Context, userID, name string) (Board, error) {
	var b Board
	err := p.pool.QueryRow(ctx, `
		insert into boards (id, owner_id, name)
		values (gen_random_uuid()::text, $1, $2)
		returning id, owner_id, name, created_at, updated_at`, userID, name).
		Scan(&b.ID, &b.OwnerID, &b.Name, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

func (p *Postgres) RenameBoard(ctx context.Context, boardID, userID, name string) error {
	tag, err := p.pool.Exec(ctx,
		`update boards set name = $3, updated_at = now() where id = $1 and owner_id = $2`,
		boardID, userID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return p.notFoundOrForbidden(ctx, boardID)
	}
	return nil
}

func (p *Postgres) DeleteBoard(ctx context.Context, boardID, userID string) error {
	tag, err := p.pool.Exec(ctx,
		`delete from boards where id = $1 and owner_id = $2`, boardID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return p.notFoundOrForbidden(ctx, boardID)
	}
	_, err = p.pool.Exec(ctx, `delete from board_updates where board_id = $1`, boardID)
	return err
}

func (p *Postgres) notFoundOrForbidden(ctx context.Context, boardID string) error {
	var exists bool
	if err := p.pool.QueryRow(ctx,
		`select exists (select 1 from boards where id = $1)`, boardID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrForbidden
	}
	return ErrNotFound
}

func (p *Postgres) CanAccess(ctx context.Context, boardID, userID string) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `
		select exists (
			select 1 from boards where id = $1 and owner_id = $2
			union all
			select 1 from board_members where board_id = $1 and user_id = $2
		)`, boardID, userID).Scan(&ok)
	return ok, err
}

func (p *Postgres) LoadUpdates(ctx context.Context, boardID string) ([][]byte, int64, error) {
	rows, err := p.pool.Query(ctx,
		`select seq, data from board_updates where board_id = $1 order by seq`, boardID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var blobs [][]byte
	var maxSeq int64
	for rows.Next() {
		var seq int64
		var data []byte
		if err := rows.Scan(&seq, &data); err != nil {
			return nil, 0, err
		}
		blobs = append(blobs, data)
		maxSeq = seq
	}
	return blobs, maxSeq, rows.Err()
}

func (p *Postgres) AppendUpdate(ctx context.Context, boardID string, blob []byte) error {
	// seq comes from a global sequence, so concurrent appends never collide.
	_, err := p.pool.Exec(ctx,
		`insert into board_updates (board_id, data) values ($1, $2)`, boardID, blob)
	return err
}

func (p *Postgres) Compact(ctx context.Context, boardID string, snapshot []byte, uptoSeq int64) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`delete from board_updates where board_id = $1 and seq <= $2`, boardID, uptoSeq); err != nil {
			return err
		}
		// The snapshot takes the (now free) high-water seq itself, sorting
		// before any update that raced in past it. The caller falls back to
		// AppendUpdate if this ever conflicts.
		_, err := tx.Exec(ctx,
			`insert into board_updates (board_id, seq, data) values ($1, $2, $3)`,
			boardID, max(uptoSeq, 1), snapshot)
		return err
	})
}
