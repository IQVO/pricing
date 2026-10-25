package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/pricing/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// pgUniqueViolation is the SQLSTATE of a unique constraint violation.
const pgUniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// nullTime stores the zero time as NULL.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// timeOrZero reads a nullable timestamp as UTC (zero when NULL).
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// readTx runs fn on the transaction carried by ctx (so a read inside a unit of
// work sees its own writes and, with lock, takes row locks), or on a read-only
// REPEATABLE READ transaction that gives fn one consistent snapshot.
func readTx(ctx context.Context, pool *pgxpool.Pool, fn func(q querier, lock string) error) error {
	if tx, ok := pgtx.From(ctx); ok {
		return fn(tx, " FOR UPDATE")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin read: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	return fn(tx, "")
}

// ItemPriceRepository is the Postgres ports.ItemPriceRepository.
type ItemPriceRepository struct{ pool *pgxpool.Pool }

var _ ports.ItemPriceRepository = (*ItemPriceRepository)(nil)

// NewItemPriceRepository constructs an ItemPriceRepository over pool.
func NewItemPriceRepository(pool *pgxpool.Pool) *ItemPriceRepository {
	return &ItemPriceRepository{pool: pool}
}

// Get implements ports.ItemPriceRepository. Inside a unit of work the row is
// locked (FOR UPDATE) so concurrent writers of one item serialise.
func (r *ItemPriceRepository) Get(ctx context.Context, id itemprice.ID) (*itemprice.ItemPrice, error) {
	var out *itemprice.ItemPrice
	err := readTx(ctx, r.pool, func(q querier, lock string) error {
		prices, err := loadItemPrices(ctx, q, lock, `WHERE catalog_item_id = $1`, string(id))
		if err != nil {
			return err
		}
		if len(prices) == 0 {
			return fmt.Errorf("item price %s: %w", id, ports.ErrNotFound)
		}
		out = prices[0]
		return nil
	})
	return out, err
}

// List implements ports.ItemPriceRepository.
func (r *ItemPriceRepository) List(ctx context.Context, after itemprice.ID, limit int) ([]*itemprice.ItemPrice, error) {
	var out []*itemprice.ItemPrice
	err := readTx(ctx, r.pool, func(q querier, _ string) error {
		prices, err := loadItemPrices(ctx, q, "", `WHERE catalog_item_id > $1 ORDER BY catalog_item_id LIMIT $2`, string(after), limit)
		out = prices
		return err
	})
	return out, err
}

// loadItemPrices reads the rows matching the clause (which may carry ORDER BY
// and LIMIT) and their sale windows.
func loadItemPrices(ctx context.Context, q querier, lock, clause string, args ...any) ([]*itemprice.ItemPrice, error) {
	rows, err := q.Query(ctx, `
		SELECT catalog_item_id, state, currency, list_price_minor, version, evaluated_at,
		       published_list_minor, published_effective_minor, published_sale_ends_at
		FROM item_prices `+clause+lock, args...)
	if err != nil {
		return nil, fmt.Errorf("read item prices: %w", err)
	}
	var snaps []itemprice.Snapshot
	for rows.Next() {
		var (
			s            itemprice.Snapshot
			state        string
			currency     string
			evaluatedAt  time.Time
			saleEndsAt   *time.Time
			listMinor    int64
			effectiveMin int64
			pubList      int64
		)
		if err := rows.Scan(&s.ID, &state, &currency, &listMinor, &s.Version, &evaluatedAt, &pubList, &effectiveMin, &saleEndsAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan item price: %w", err)
		}
		s.State = itemprice.State(state)
		s.ListPrice = shared.Money{AmountMinor: listMinor, Currency: currency}
		s.EvaluatedAt = evaluatedAt.UTC()
		s.Published = itemprice.View{ListMinor: pubList, EffectiveMinor: effectiveMin, SaleEndsAt: timeOrZero(saleEndsAt)}
		snaps = append(snaps, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read item prices: %w", err)
	}
	rows.Close()
	out := make([]*itemprice.ItemPrice, 0, len(snaps))
	for _, s := range snaps {
		if s.Sales, err = loadSales(ctx, q, s.ID, s.ListPrice.Currency); err != nil {
			return nil, err
		}
		p, err := itemprice.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func loadSales(ctx context.Context, q querier, id itemprice.ID, currency string) ([]itemprice.SaleWindow, error) {
	rows, err := q.Query(ctx, `SELECT sale_id, price_minor, starts_at, ends_at FROM sale_windows WHERE catalog_item_id = $1 ORDER BY starts_at`, string(id))
	if err != nil {
		return nil, fmt.Errorf("read sale windows of %s: %w", id, err)
	}
	defer rows.Close()
	var sales []itemprice.SaleWindow
	for rows.Next() {
		w := itemprice.SaleWindow{Price: shared.Money{Currency: currency}}
		var from, to time.Time
		if err := rows.Scan(&w.SaleID, &w.Price.AmountMinor, &from, &to); err != nil {
			return nil, fmt.Errorf("scan sale window of %s: %w", id, err)
		}
		w.From, w.To = from.UTC(), to.UTC()
		sales = append(sales, w)
	}
	return sales, rows.Err()
}

// Save implements ports.ItemPriceRepository.
func (r *ItemPriceRepository) Save(ctx context.Context, p *itemprice.ItemPrice) error {
	s := p.Snapshot()
	err := inTx(ctx, r.pool, func(q querier) error {
		if err := writeItemPrice(ctx, q, p, s); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM sale_windows WHERE catalog_item_id = $1`, string(s.ID)); err != nil {
			return fmt.Errorf("replace sale windows of %s: %w", s.ID, err)
		}
		for _, w := range s.Sales {
			if _, err := q.Exec(ctx, `
				INSERT INTO sale_windows (catalog_item_id, sale_id, price_minor, starts_at, ends_at) VALUES ($1, $2, $3, $4, $5)`,
				string(s.ID), w.SaleID, w.Price.AmountMinor, w.From.UTC(), w.To.UTC()); err != nil {
				return fmt.Errorf("insert sale window %s of %s: %w", w.SaleID, s.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	p.MarkPersisted()
	return nil
}

// writeItemPrice inserts the row of a new item price or updates the one at the
// loaded version; anything else is ports.ErrConcurrentUpdate.
func writeItemPrice(ctx context.Context, q querier, p *itemprice.ItemPrice, s itemprice.Snapshot) error {
	conflict := fmt.Errorf("item price %s loaded at version %d: %w", s.ID, p.LoadedVersion(), ports.ErrConcurrentUpdate)
	next := nullTime(s.NextBoundary())
	if p.LoadedVersion() == 0 {
		_, err := q.Exec(ctx, `
			INSERT INTO item_prices (catalog_item_id, state, currency, list_price_minor, version, evaluated_at,
				published_list_minor, published_effective_minor, published_sale_ends_at, next_boundary_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			string(s.ID), string(s.State), s.ListPrice.Currency, s.ListPrice.AmountMinor, s.Version, s.EvaluatedAt.UTC(),
			s.Published.ListMinor, s.Published.EffectiveMinor, nullTime(s.Published.SaleEndsAt), next)
		if isUniqueViolation(err) {
			return conflict
		}
		return err
	}
	tag, err := q.Exec(ctx, `
		UPDATE item_prices SET state = $2, currency = $3, list_price_minor = $4, version = $5, evaluated_at = $6,
			published_list_minor = $7, published_effective_minor = $8, published_sale_ends_at = $9, next_boundary_at = $10
		WHERE catalog_item_id = $1 AND version = $11`,
		string(s.ID), string(s.State), s.ListPrice.Currency, s.ListPrice.AmountMinor, s.Version, s.EvaluatedAt.UTC(),
		s.Published.ListMinor, s.Published.EffectiveMinor, nullTime(s.Published.SaleEndsAt), next, p.LoadedVersion())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return conflict
	}
	return nil
}

// DueForBoundary implements ports.ItemPriceRepository.
func (r *ItemPriceRepository) DueForBoundary(ctx context.Context, now time.Time, limit int) ([]itemprice.ID, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT catalog_item_id FROM item_prices
		WHERE state = 'ACTIVE' AND next_boundary_at <= $1
		ORDER BY next_boundary_at, catalog_item_id LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("scan item prices due for a boundary: %w", err)
	}
	defer rows.Close()
	var ids []itemprice.ID
	for rows.Next() {
		var id itemprice.ID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan item price id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CatalogView is the Postgres ports.CatalogView over catalog_items.
type CatalogView struct{ pool *pgxpool.Pool }

var _ ports.CatalogView = (*CatalogView)(nil)

// NewCatalogView constructs a CatalogView over pool.
func NewCatalogView(pool *pgxpool.Pool) *CatalogView { return &CatalogView{pool: pool} }

// State implements ports.CatalogView.
func (c *CatalogView) State(ctx context.Context, id itemprice.ID) (itemprice.CatalogState, error) {
	var state string
	err := queryFor(ctx, c.pool).QueryRow(ctx, `SELECT state FROM catalog_items WHERE catalog_item_id = $1`, string(id)).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return itemprice.CatalogUnknown, nil
	}
	if err != nil {
		return "", fmt.Errorf("read catalog item %s: %w", id, err)
	}
	return itemprice.CatalogState(state), nil
}

// MarkCreated implements ports.CatalogView.
func (c *CatalogView) MarkCreated(ctx context.Context, id itemprice.ID) error {
	return inTx(ctx, c.pool, func(q querier) error {
		_, err := q.Exec(ctx, `INSERT INTO catalog_items (catalog_item_id, state) VALUES ($1, 'ACTIVE') ON CONFLICT (catalog_item_id) DO NOTHING`, string(id))
		return err
	})
}

// MarkDiscontinued implements ports.CatalogView.
func (c *CatalogView) MarkDiscontinued(ctx context.Context, id itemprice.ID) error {
	return inTx(ctx, c.pool, func(q querier) error {
		_, err := q.Exec(ctx, `
			INSERT INTO catalog_items (catalog_item_id, state) VALUES ($1, 'DISCONTINUED')
			ON CONFLICT (catalog_item_id) DO UPDATE SET state = 'DISCONTINUED', updated_at = now()`, string(id))
		return err
	})
}
