package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Order struct {
	ID          int64      `json:"id"`
	Item        string     `json:"item"`
	Quantity    int        `json:"quantity"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

type Store struct {
	pool *pgxpool.Pool
}

// Advisory lock id, чтобы несколько реплик не гоняли миграцию одновременно.
const migrateLockID = 424242

const schema = `
CREATE TABLE IF NOT EXISTS orders (
	id           BIGSERIAL PRIMARY KEY,
	item         TEXT        NOT NULL,
	quantity     INT         NOT NULL CHECK (quantity > 0),
	status       TEXT        NOT NULL DEFAULT 'new',
	created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	processed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS orders_status_idx ON orders (status);
`

// Connect ждёт, пока база станет доступна, и применяет схему.
func Connect(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			break
		}
		slog.Warn("database not ready", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrateLockID); err != nil {
		return fmt.Errorf("migrate lock: %w", err)
	}
	if _, err := tx.Exec(ctx, schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) CreateOrder(ctx context.Context, item string, quantity int) (Order, error) {
	var o Order
	err := s.pool.QueryRow(ctx,
		`INSERT INTO orders (item, quantity) VALUES ($1, $2)
		 RETURNING id, item, quantity, status, created_at, processed_at`,
		item, quantity,
	).Scan(&o.ID, &o.Item, &o.Quantity, &o.Status, &o.CreatedAt, &o.ProcessedAt)
	return o, err
}

func (s *Store) ListOrders(ctx context.Context, limit int) ([]Order, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, item, quantity, status, created_at, processed_at
		 FROM orders ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := make([]Order, 0)
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.Item, &o.Quantity, &o.Status, &o.CreatedAt, &o.ProcessedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// ProcessBatch помечает до batch новых заказов обработанными.
// SKIP LOCKED позволяет нескольким репликам worker не брать одни и те же строки.
func (s *Store) ProcessBatch(ctx context.Context, batch int) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE orders SET status = 'processed', processed_at = now()
		 WHERE id IN (
			SELECT id FROM orders WHERE status = 'new'
			ORDER BY id LIMIT $1
			FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) CountPending(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE status = 'new'`).Scan(&n)
	return n, err
}
