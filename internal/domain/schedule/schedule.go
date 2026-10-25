// Package schedule is the DeliveryChargeSchedule aggregate: the delivery
// charge of each service tier (SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD), with an
// optional free-over threshold per tier. Exactly one schedule is CURRENT;
// publishing a new one supersedes the previous in the same transaction (states
// CURRENT -> SUPERSEDED, model-gap decision 4) and its version must strictly
// increase (ErrNonIncreasingVersion). A schedule names a charge for every tier
// (ErrIncompleteChargeSchedule). A new version supersedes, never edits.
package schedule

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/pricing/internal/domain/shared"
)

const entity = "deliverychargeschedule"

var (
	// ErrIncompleteChargeSchedule: a schedule names a charge for every tier
	// (422 incomplete-charge-schedule).
	ErrIncompleteChargeSchedule = errors.New("incomplete charge schedule")
	// ErrNonIncreasingVersion: a new version is higher than the current one
	// (409 non-increasing-version).
	ErrNonIncreasingVersion = errors.New("non-increasing version")
)

// ID identifies a schedule (scheduleId).
type ID string

// State is the lifecycle state of a schedule.
type State string

// The two states.
const (
	StateCurrent    State = "CURRENT"
	StateSuperseded State = "SUPERSEDED"
)

// Valid reports whether s is one of the two states.
func (s State) Valid() bool { return s == StateCurrent || s == StateSuperseded }

// TierCharge is the charge of one tier. FreeOver is the optional threshold:
// a group whose goods total reaches it ships free; the zero Money means none.
type TierCharge struct {
	Tier     shared.Tier
	Charge   shared.Money
	FreeOver shared.Money
}

// DeliveryChargeSchedule is the aggregate root.
type DeliveryChargeSchedule struct {
	id          ID
	version     int64
	state       State
	tiers       []TierCharge
	publishedAt time.Time
}

// ID returns the scheduleId.
func (s *DeliveryChargeSchedule) ID() ID { return s.id }

// Version returns the schedule version published by the merchandiser.
func (s *DeliveryChargeSchedule) Version() int64 { return s.version }

// State returns the lifecycle state.
func (s *DeliveryChargeSchedule) State() State { return s.state }

// Tiers returns a copy of the tier charges in speed order.
func (s *DeliveryChargeSchedule) Tiers() []TierCharge { return slices.Clone(s.tiers) }

// PublishedAt returns when the schedule was published.
func (s *DeliveryChargeSchedule) PublishedAt() time.Time { return s.publishedAt }

// Publish creates the CURRENT schedule of the given version. currentVersion is
// the version of the schedule being replaced, 0 when there is none; the caller
// supersedes that schedule (Supersede) in the same unit of work. It raises
// DeliveryChargeScheduleChanged.
func Publish(id ID, version int64, tiers []TierCharge, currentVersion int64, currency string, now time.Time) (*DeliveryChargeSchedule, []shared.Event, error) {
	if !shared.ValidID(string(id)) {
		return nil, nil, shared.Invalid("scheduleId %q is not a valid id", id)
	}
	if version < 1 || version > shared.MaxInt32 {
		return nil, nil, shared.Invalid("version must be between 1 and %d", shared.MaxInt32)
	}
	ordered, err := orderTiers(tiers, currency)
	if err != nil {
		return nil, nil, err
	}
	if version <= currentVersion {
		return nil, nil, fmt.Errorf("%w: version %d does not exceed the current version %d", ErrNonIncreasingVersion, version, currentVersion)
	}
	s := &DeliveryChargeSchedule{id: id, version: version, state: StateCurrent, tiers: ordered, publishedAt: shared.UTC(now)}
	return s, []shared.Event{DeliveryChargeScheduleChanged{
		Header:     shared.Header{Version: version, At: s.publishedAt},
		ScheduleID: id,
		Tiers:      slices.Clone(ordered),
	}}, nil
}

// orderTiers validates the charges and returns them in speed order.
func orderTiers(tiers []TierCharge, currency string) ([]TierCharge, error) {
	byTier := make(map[shared.Tier]TierCharge, len(tiers))
	for _, tc := range tiers {
		if !tc.Tier.Valid() {
			return nil, shared.Invalid("tier %q is not one of SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD", tc.Tier)
		}
		if _, dup := byTier[tc.Tier]; dup {
			return nil, shared.Invalid("tier %s is named twice", tc.Tier)
		}
		if err := tc.Charge.Check(currency); err != nil {
			return nil, fmt.Errorf("tier %s charge: %w", tc.Tier, err)
		}
		if !tc.FreeOver.IsZero() {
			if err := tc.FreeOver.Check(currency); err != nil {
				return nil, fmt.Errorf("tier %s freeOver: %w", tc.Tier, err)
			}
		}
		byTier[tc.Tier] = tc
	}
	out := make([]TierCharge, 0, len(byTier))
	for _, tier := range shared.Tiers() {
		tc, ok := byTier[tier]
		if !ok {
			return nil, fmt.Errorf("%w: tier %s has no charge", ErrIncompleteChargeSchedule, tier)
		}
		out = append(out, tc)
	}
	return out, nil
}

// Supersede moves CURRENT to SUPERSEDED. A superseded schedule cannot be
// superseded again.
func (s *DeliveryChargeSchedule) Supersede() error {
	if s.state != StateCurrent {
		return fmt.Errorf("%w: schedule %s is already %s", shared.ErrIllegalTransition, s.id, s.state)
	}
	s.state = StateSuperseded
	return nil
}

// ChargeFor returns the delivery charge of a shipment group of the tier whose
// goods are worth goods: zero when the tier has a free-over threshold and the
// goods reach it, the tier charge otherwise.
func (s *DeliveryChargeSchedule) ChargeFor(tier shared.Tier, goods shared.Money) (shared.Money, error) {
	for _, tc := range s.tiers {
		if tc.Tier != tier {
			continue
		}
		if !tc.FreeOver.IsZero() && goods.AmountMinor >= tc.FreeOver.AmountMinor {
			return shared.Money{AmountMinor: 0, Currency: tc.Charge.Currency}, nil
		}
		return tc.Charge, nil
	}
	return shared.Money{}, shared.Invalid("tier %q is not one of SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD", tier)
}

// Snapshot is the persisted form of a schedule.
type Snapshot struct {
	ID          ID
	Version     int64
	State       State
	Tiers       []TierCharge
	PublishedAt time.Time
}

// Snapshot returns the persisted form of the schedule.
func (s *DeliveryChargeSchedule) Snapshot() Snapshot {
	return Snapshot{ID: s.id, Version: s.version, State: s.state, Tiers: slices.Clone(s.tiers), PublishedAt: s.publishedAt}
}

// Restore rebuilds a persisted schedule, validating every invariant.
func Restore(snap Snapshot) (*DeliveryChargeSchedule, error) {
	if !shared.ValidID(string(snap.ID)) {
		return nil, shared.Corrupt("schedule %q: scheduleId must be a valid id", snap.ID)
	}
	if snap.Version < 1 || snap.Version > shared.MaxInt32 || !snap.State.Valid() || snap.PublishedAt.IsZero() {
		return nil, shared.Corrupt("schedule %q: version, state and publishedAt are required", snap.ID)
	}
	if len(snap.Tiers) == 0 {
		return nil, shared.Corrupt("schedule %q: no tiers", snap.ID)
	}
	currency := snap.Tiers[0].Charge.Currency
	if !shared.ValidCurrency(currency) {
		return nil, shared.Corrupt("schedule %q: currency %q is not an ISO 4217 code", snap.ID, currency)
	}
	ordered, err := orderTiers(snap.Tiers, currency)
	if err != nil {
		return nil, shared.Corrupt("schedule %q: %v", snap.ID, err)
	}
	return &DeliveryChargeSchedule{id: snap.ID, version: snap.Version, state: snap.State, tiers: ordered, publishedAt: shared.UTC(snap.PublishedAt)}, nil
}

// DeliveryChargeScheduleChanged is raised by Publish. Version is the schedule
// version, strictly increasing.
type DeliveryChargeScheduleChanged struct {
	shared.Header
	ScheduleID ID
	Tiers      []TierCharge
}

// EventName returns "DeliveryChargeScheduleChanged".
func (DeliveryChargeScheduleChanged) EventName() string { return "DeliveryChargeScheduleChanged" }

// Entity returns "deliverychargeschedule".
func (DeliveryChargeScheduleChanged) Entity() string { return entity }

// SubjectID returns the scheduleId.
func (e DeliveryChargeScheduleChanged) SubjectID() string { return string(e.ScheduleID) }
