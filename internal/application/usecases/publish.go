package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/views"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// PublishChargeSchedule is the command PublishChargeSchedule
// (POST /delivery-charge-schedules): the new schedule becomes CURRENT and the
// previous one SUPERSEDED in the same transaction.
type PublishChargeSchedule struct{ Deps }

// PublishChargeScheduleInput is the body of POST /delivery-charge-schedules.
type PublishChargeScheduleInput struct {
	Version int64
	Tiers   []schedule.TierCharge
}

// Execute publishes the schedule and DeliveryChargeScheduleChanged.
func (u PublishChargeSchedule) Execute(ctx context.Context, in PublishChargeScheduleInput) (views.ChargeSchedule, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.ChargeSchedule, error) {
		cur, err := u.Schedules.Current(ctx)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return views.ChargeSchedule{}, err
		}
		var currentVersion int64
		if cur != nil {
			currentVersion = cur.Version()
		}
		s, events, err := schedule.Publish(u.IDs.NewScheduleID(), in.Version, in.Tiers, currentVersion, u.Currency, u.now())
		if err != nil {
			return views.ChargeSchedule{}, err
		}
		if cur != nil {
			if err := cur.Supersede(); err != nil {
				return views.ChargeSchedule{}, err
			}
			if err := u.Schedules.Save(ctx, cur); err != nil {
				return views.ChargeSchedule{}, err
			}
		}
		if err := u.Schedules.Save(ctx, s); err != nil {
			return views.ChargeSchedule{}, err
		}
		if err := u.publish(ctx, events); err != nil {
			return views.ChargeSchedule{}, err
		}
		return views.ChargeScheduleFrom(s), nil
	})
}

// PublishTaxTable is the command PublishTaxTable (POST /tax-tables): the new
// table becomes CURRENT and the previous one SUPERSEDED in the same
// transaction.
type PublishTaxTable struct{ Deps }

// PublishTaxTableInput is the body of POST /tax-tables.
type PublishTaxTableInput struct {
	Version int64
	Bands   []tax.TaxBand
}

// Execute publishes the table and TaxTableChanged.
func (u PublishTaxTable) Execute(ctx context.Context, in PublishTaxTableInput) (views.TaxTable, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.TaxTable, error) {
		cur, err := u.TaxTables.Current(ctx)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return views.TaxTable{}, err
		}
		var currentVersion int64
		if cur != nil {
			currentVersion = cur.Version()
		}
		t, events, err := tax.Publish(u.IDs.NewTaxTableID(), in.Version, in.Bands, currentVersion, u.now())
		if err != nil {
			return views.TaxTable{}, err
		}
		if cur != nil {
			if err := cur.Supersede(); err != nil {
				return views.TaxTable{}, err
			}
			if err := u.TaxTables.Save(ctx, cur); err != nil {
				return views.TaxTable{}, err
			}
		}
		if err := u.TaxTables.Save(ctx, t); err != nil {
			return views.TaxTable{}, err
		}
		if err := u.publish(ctx, events); err != nil {
			return views.TaxTable{}, err
		}
		return views.TaxTableFrom(t), nil
	})
}
