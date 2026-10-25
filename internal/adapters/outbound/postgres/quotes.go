package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// QuoteRepository is the Postgres ports.QuoteRepository over price_quotes,
// quote_lines and quote_groups. A quote is immutable: only its state and
// version change after the insert.
type QuoteRepository struct{ pool *pgxpool.Pool }

var _ ports.QuoteRepository = (*QuoteRepository)(nil)

// NewQuoteRepository constructs a QuoteRepository over pool.
func NewQuoteRepository(pool *pgxpool.Pool) *QuoteRepository { return &QuoteRepository{pool: pool} }

// Get implements ports.QuoteRepository.
func (r *QuoteRepository) Get(ctx context.Context, id quote.ID) (*quote.PriceQuote, error) {
	var out *quote.PriceQuote
	err := readTx(ctx, r.pool, func(q querier, lock string) error {
		var (
			s                  quote.Snapshot
			state              string
			issuedAt, expireAt time.Time
		)
		err := q.QueryRow(ctx, `
			SELECT quote_id, cart_id, state, currency, tax_minor, total_minor, schedule_version, tax_table_version, issued_at, expires_at, version
			FROM price_quotes WHERE quote_id = $1`+lock, string(id)).
			Scan(&s.ID, &s.CartID, &state, &s.Currency, &s.TaxMinor, &s.TotalMinor, &s.ScheduleVersion, &s.TaxTableVersion, &issuedAt, &expireAt, &s.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("quote %s: %w", id, ports.ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("read quote %s: %w", id, err)
		}
		s.State, s.IssuedAt, s.ExpiresAt = quote.State(state), issuedAt.UTC(), expireAt.UTC()
		if s.Lines, err = loadQuoteLines(ctx, q, id, s.Currency); err != nil {
			return err
		}
		if s.Groups, err = loadQuoteGroups(ctx, q, id, s.Currency); err != nil {
			return err
		}
		out, err = quote.Restore(s)
		return err
	})
	return out, err
}

func loadQuoteLines(ctx context.Context, q querier, id quote.ID, currency string) ([]quote.Line, error) {
	rows, err := q.Query(ctx, `
		SELECT line_no, catalog_item_id, quantity, unit_price_minor, line_total_minor, price_version
		FROM quote_lines WHERE quote_id = $1 ORDER BY line_no`, string(id))
	if err != nil {
		return nil, fmt.Errorf("read lines of quote %s: %w", id, err)
	}
	defer rows.Close()
	var lines []quote.Line
	for rows.Next() {
		var l quote.Line
		var unit, total int64
		if err := rows.Scan(&l.LineNo, &l.CatalogItemID, &l.Quantity, &unit, &total, &l.PriceVersion); err != nil {
			return nil, fmt.Errorf("scan line of quote %s: %w", id, err)
		}
		l.UnitPrice = shared.Money{AmountMinor: unit, Currency: currency}
		l.LineTotal = shared.Money{AmountMinor: total, Currency: currency}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func loadQuoteGroups(ctx context.Context, q querier, id quote.ID, currency string) ([]quote.Group, error) {
	rows, err := q.Query(ctx, `SELECT group_no, tier, delivery_charge_minor FROM quote_groups WHERE quote_id = $1 ORDER BY group_no`, string(id))
	if err != nil {
		return nil, fmt.Errorf("read groups of quote %s: %w", id, err)
	}
	defer rows.Close()
	var groups []quote.Group
	for rows.Next() {
		var g quote.Group
		var tier string
		var charge int64
		if err := rows.Scan(&g.GroupNo, &tier, &charge); err != nil {
			return nil, fmt.Errorf("scan group of quote %s: %w", id, err)
		}
		g.Tier, g.DeliveryCharge = shared.Tier(tier), shared.Money{AmountMinor: charge, Currency: currency}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// Save implements ports.QuoteRepository.
func (r *QuoteRepository) Save(ctx context.Context, q *quote.PriceQuote) error {
	s := q.Snapshot()
	conflict := fmt.Errorf("quote %s loaded at version %d: %w", s.ID, q.LoadedVersion(), ports.ErrConcurrentUpdate)
	err := inTx(ctx, r.pool, func(tx querier) error {
		if q.LoadedVersion() > 0 {
			tag, err := tx.Exec(ctx, `UPDATE price_quotes SET state = $2, version = $3 WHERE quote_id = $1 AND version = $4`,
				string(s.ID), string(s.State), s.Version, q.LoadedVersion())
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return conflict
			}
			return nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO price_quotes (quote_id, cart_id, state, currency, tax_minor, total_minor, schedule_version, tax_table_version, issued_at, expires_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			string(s.ID), s.CartID, string(s.State), s.Currency, s.TaxMinor, s.TotalMinor, s.ScheduleVersion, s.TaxTableVersion,
			s.IssuedAt.UTC(), s.ExpiresAt.UTC(), s.Version); err != nil {
			if isUniqueViolation(err) {
				return conflict
			}
			return err
		}
		return insertQuoteRows(ctx, tx, s)
	})
	if err != nil {
		return err
	}
	q.MarkPersisted()
	return nil
}

func insertQuoteRows(ctx context.Context, tx querier, s quote.Snapshot) error {
	for _, l := range s.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO quote_lines (quote_id, line_no, catalog_item_id, quantity, unit_price_minor, line_total_minor, price_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			string(s.ID), l.LineNo, l.CatalogItemID, l.Quantity, l.UnitPrice.AmountMinor, l.LineTotal.AmountMinor, l.PriceVersion); err != nil {
			return fmt.Errorf("insert line %d of quote %s: %w", l.LineNo, s.ID, err)
		}
	}
	for _, g := range s.Groups {
		if _, err := tx.Exec(ctx, `INSERT INTO quote_groups (quote_id, group_no, tier, delivery_charge_minor) VALUES ($1, $2, $3, $4)`,
			string(s.ID), g.GroupNo, string(g.Tier), g.DeliveryCharge.AmountMinor); err != nil {
			return fmt.Errorf("insert group %d of quote %s: %w", g.GroupNo, s.ID, err)
		}
	}
	return nil
}

// DueForExpiry implements ports.QuoteRepository.
func (r *QuoteRepository) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]quote.ID, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT quote_id FROM price_quotes WHERE state = 'ISSUED' AND expires_at <= $1
		ORDER BY expires_at, quote_id LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("scan quotes due for expiry: %w", err)
	}
	defer rows.Close()
	var ids []quote.ID
	for rows.Next() {
		var id quote.ID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan quote id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
