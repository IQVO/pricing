package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// ScheduleRepository is the Postgres ports.ScheduleRepository over
// delivery_charge_schedules and tier_charges. The unique index on version and
// the partial unique index on state = 'CURRENT' enforce "versions strictly
// increase" and "exactly one is current" even against concurrent publishers.
type ScheduleRepository struct{ pool *pgxpool.Pool }

var _ ports.ScheduleRepository = (*ScheduleRepository)(nil)

// NewScheduleRepository constructs a ScheduleRepository over pool.
func NewScheduleRepository(pool *pgxpool.Pool) *ScheduleRepository {
	return &ScheduleRepository{pool: pool}
}

// queryCurrent scans the single CURRENT row of a versioned pricing table into
// dest. "No such row" is ports.ErrNotFound and real read failures keep their
// cause, each named after the entity for runbook greppability.
func queryCurrent(ctx context.Context, q querier, sql, entity string, dest ...any) error {
	err := q.QueryRow(ctx, sql).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("current %s: %w", entity, ports.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read the current %s: %w", entity, err)
	}
	return nil
}

// currentInTx reads the CURRENT row of a versioned table inside one read
// transaction: read scans the row and loads the aggregate's detail rows.
func currentInTx[T any](ctx context.Context, pool *pgxpool.Pool, read func(querier) (T, error)) (T, error) {
	var out T
	err := readTx(ctx, pool, func(q querier, _ string) error {
		var err error
		out, err = read(q)
		return err
	})
	return out, err
}

// Current implements ports.ScheduleRepository.
func (r *ScheduleRepository) Current(ctx context.Context) (*schedule.DeliveryChargeSchedule, error) {
	return currentInTx(ctx, r.pool, func(q querier) (*schedule.DeliveryChargeSchedule, error) {
		var (
			s           schedule.Snapshot
			state       string
			publishedAt time.Time
		)
		if err := queryCurrent(ctx, q,
			`SELECT schedule_id, version, state, published_at FROM delivery_charge_schedules WHERE state = 'CURRENT'`,
			"delivery charge schedule", &s.ID, &s.Version, &state, &publishedAt); err != nil {
			return nil, err
		}
		s.State, s.PublishedAt = schedule.State(state), publishedAt.UTC()
		tiers, err := loadTiers(ctx, q, s.ID)
		if err != nil {
			return nil, err
		}
		s.Tiers = tiers
		return schedule.Restore(s)
	})
}

func loadTiers(ctx context.Context, q querier, id schedule.ID) ([]schedule.TierCharge, error) {
	rows, err := q.Query(ctx, `SELECT tier, currency, charge_minor, free_over_minor FROM tier_charges WHERE schedule_id = $1`, string(id))
	if err != nil {
		return nil, fmt.Errorf("read tier charges of %s: %w", id, err)
	}
	defer rows.Close()
	var tiers []schedule.TierCharge
	for rows.Next() {
		var (
			tier, currency string
			charge         int64
			freeOver       *int64
		)
		if err := rows.Scan(&tier, &currency, &charge, &freeOver); err != nil {
			return nil, fmt.Errorf("scan tier charge of %s: %w", id, err)
		}
		tc := schedule.TierCharge{Tier: shared.Tier(tier), Charge: shared.Money{AmountMinor: charge, Currency: currency}}
		if freeOver != nil {
			tc.FreeOver = shared.Money{AmountMinor: *freeOver, Currency: currency}
		}
		tiers = append(tiers, tc)
	}
	return tiers, rows.Err()
}

// Save implements ports.ScheduleRepository.
func (r *ScheduleRepository) Save(ctx context.Context, sc *schedule.DeliveryChargeSchedule) error {
	s := sc.Snapshot()
	return inTx(ctx, r.pool, func(q querier) error {
		tag, err := q.Exec(ctx, `UPDATE delivery_charge_schedules SET state = $2 WHERE schedule_id = $1`, string(s.ID), string(s.State))
		if err != nil {
			return fmt.Errorf("update schedule %s: %w", s.ID, err)
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO delivery_charge_schedules (schedule_id, version, state, published_at) VALUES ($1, $2, $3, $4)`,
			string(s.ID), s.Version, string(s.State), s.PublishedAt.UTC()); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("schedule %s version %d: %w", s.ID, s.Version, ports.ErrConcurrentUpdate)
			}
			return fmt.Errorf("insert schedule %s: %w", s.ID, err)
		}
		for _, tc := range s.Tiers {
			var freeOver *int64
			if !tc.FreeOver.IsZero() {
				freeOver = &tc.FreeOver.AmountMinor
			}
			if _, err := q.Exec(ctx, `INSERT INTO tier_charges (schedule_id, tier, currency, charge_minor, free_over_minor) VALUES ($1, $2, $3, $4, $5)`,
				string(s.ID), string(tc.Tier), tc.Charge.Currency, tc.Charge.AmountMinor, freeOver); err != nil {
				return fmt.Errorf("insert tier %s of %s: %w", tc.Tier, s.ID, err)
			}
		}
		return nil
	})
}

// TaxTableRepository is the Postgres ports.TaxTableRepository over tax_tables
// and tax_bands, with the same unique indexes as the schedules.
type TaxTableRepository struct{ pool *pgxpool.Pool }

var _ ports.TaxTableRepository = (*TaxTableRepository)(nil)

// NewTaxTableRepository constructs a TaxTableRepository over pool.
func NewTaxTableRepository(pool *pgxpool.Pool) *TaxTableRepository {
	return &TaxTableRepository{pool: pool}
}

// Current implements ports.TaxTableRepository.
func (r *TaxTableRepository) Current(ctx context.Context) (*tax.TaxTable, error) {
	return currentInTx(ctx, r.pool, func(q querier) (*tax.TaxTable, error) {
		var (
			s           tax.Snapshot
			state       string
			publishedAt time.Time
		)
		if err := queryCurrent(ctx, q,
			`SELECT tax_table_id, version, state, published_at FROM tax_tables WHERE state = 'CURRENT'`,
			"tax table", &s.ID, &s.Version, &state, &publishedAt); err != nil {
			return nil, err
		}
		s.State, s.PublishedAt = tax.State(state), publishedAt.UTC()
		bands, err := loadBands(ctx, q, s.ID)
		if err != nil {
			return nil, err
		}
		s.Bands = bands
		return tax.Restore(s)
	})
}

func loadBands(ctx context.Context, q querier, id tax.ID) ([]tax.TaxBand, error) {
	rows, err := q.Query(ctx, `SELECT postal_prefix_from, postal_prefix_to, rate_basis_points FROM tax_bands WHERE tax_table_id = $1 ORDER BY postal_prefix_from`, string(id))
	if err != nil {
		return nil, fmt.Errorf("read tax bands of %s: %w", id, err)
	}
	defer rows.Close()
	var bands []tax.TaxBand
	for rows.Next() {
		var b tax.TaxBand
		if err := rows.Scan(&b.PostalPrefixFrom, &b.PostalPrefixTo, &b.RateBasisPoints); err != nil {
			return nil, fmt.Errorf("scan tax band of %s: %w", id, err)
		}
		bands = append(bands, b)
	}
	return bands, rows.Err()
}

// Save implements ports.TaxTableRepository.
func (r *TaxTableRepository) Save(ctx context.Context, t *tax.TaxTable) error {
	s := t.Snapshot()
	return inTx(ctx, r.pool, func(q querier) error {
		tag, err := q.Exec(ctx, `UPDATE tax_tables SET state = $2 WHERE tax_table_id = $1`, string(s.ID), string(s.State))
		if err != nil {
			return fmt.Errorf("update tax table %s: %w", s.ID, err)
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO tax_tables (tax_table_id, version, state, published_at) VALUES ($1, $2, $3, $4)`,
			string(s.ID), s.Version, string(s.State), s.PublishedAt.UTC()); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("tax table %s version %d: %w", s.ID, s.Version, ports.ErrConcurrentUpdate)
			}
			return fmt.Errorf("insert tax table %s: %w", s.ID, err)
		}
		for _, b := range s.Bands {
			if _, err := q.Exec(ctx, `INSERT INTO tax_bands (tax_table_id, postal_prefix_from, postal_prefix_to, rate_basis_points) VALUES ($1, $2, $3, $4)`,
				string(s.ID), b.PostalPrefixFrom, b.PostalPrefixTo, b.RateBasisPoints); err != nil {
				return fmt.Errorf("insert band %s-%s of %s: %w", b.PostalPrefixFrom, b.PostalPrefixTo, s.ID, err)
			}
		}
		return nil
	})
}
