package schedule_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
)

const cur = "BRL"

var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

func brl(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: cur} }

func fullTiers() []schedule.TierCharge {
	return []schedule.TierCharge{
		{Tier: shared.TierStandard, Charge: brl(0), FreeOver: brl(15000)},
		{Tier: shared.TierSameDay, Charge: brl(2990)},
		{Tier: shared.TierTwoDay, Charge: brl(990)},
		{Tier: shared.TierNextDay, Charge: brl(1490), FreeOver: brl(30000)},
	}
}

type check struct {
	name string
	ok   bool
}

// verify reports every failed check, naming it and dumping got.
func verify(t *testing.T, what string, got any, checks ...check) {
	t.Helper()
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: %s is wrong in %+v", what, c.name, got)
		}
	}
}

func mustPublish(t *testing.T, version int64) *schedule.DeliveryChargeSchedule {
	t.Helper()
	s, _, err := schedule.Publish("sch-1", version, fullTiers(), 0, cur, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPublishCreatesTheCurrentScheduleAndRaisesTheEvent(t *testing.T) {
	s, evs, err := schedule.Publish("sch-1", 3, fullTiers(), 2, cur, t0.Add(123*time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	verify(t, "schedule", s,
		check{"ID", s.ID() == "sch-1"},
		check{"Version", s.Version() == 3},
		check{"State", s.State() == schedule.StateCurrent},
		check{"PublishedAt", s.PublishedAt().Equal(t0)},
	)
	tiers := s.Tiers()
	want := []shared.Tier{shared.TierSameDay, shared.TierNextDay, shared.TierTwoDay, shared.TierStandard}
	for i, tier := range want {
		if tiers[i].Tier != tier {
			t.Fatalf("Tiers()[%d] = %s, want %s (speed order)", i, tiers[i].Tier, tier)
		}
	}
	verify(t, "free-over thresholds", tiers,
		check{"next day", tiers[1].FreeOver == brl(30000)},
		check{"same day has none", tiers[0].FreeOver.IsZero()},
	)
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	e, ok := evs[0].(schedule.DeliveryChargeScheduleChanged)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	verify(t, "event", e,
		check{"ScheduleID", e.ScheduleID == "sch-1"},
		check{"SubjectID", e.SubjectID() == "sch-1"},
		check{"AggregateVersion", e.AggregateVersion() == 3},
		check{"OccurredAt", e.OccurredAt().Equal(t0)},
		check{"EventName", e.EventName() == "DeliveryChargeScheduleChanged"},
		check{"Entity", e.Entity() == "deliverychargeschedule"},
		check{"Tiers", len(e.Tiers) == 4},
	)
	// the event does not alias the aggregate.
	e.Tiers[0].Charge.AmountMinor = 1
	if s.Tiers()[0].Charge.AmountMinor == 1 {
		t.Error("the event shares its tiers with the aggregate")
	}
	s.Tiers()[0].Charge.AmountMinor = 7
	if s.Tiers()[0].Charge.AmountMinor == 7 {
		t.Error("Tiers() returns the aggregate's own slice")
	}
}

func TestPublishRefusals(t *testing.T) {
	without := func(drop shared.Tier) []schedule.TierCharge {
		var out []schedule.TierCharge
		for _, tc := range fullTiers() {
			if tc.Tier != drop {
				out = append(out, tc)
			}
		}
		return out
	}
	with := func(f func(*[]schedule.TierCharge)) []schedule.TierCharge {
		tiers := fullTiers()
		f(&tiers)
		return tiers
	}
	cases := []struct {
		name    string
		id      schedule.ID
		version int64
		tiers   []schedule.TierCharge
		current int64
		want    error
	}{
		{"bad id", "bad id", 2, fullTiers(), 1, shared.ErrValidation},
		{"version zero", "s", 0, fullTiers(), 0, shared.ErrValidation},
		{"version above the integer ceiling", "s", shared.MaxInt32 + 1, fullTiers(), 0, shared.ErrValidation},
		{"no tiers", "s", 2, nil, 1, schedule.ErrIncompleteChargeSchedule},
		{"missing same day", "s", 2, without(shared.TierSameDay), 1, schedule.ErrIncompleteChargeSchedule},
		{"missing standard", "s", 2, without(shared.TierStandard), 1, schedule.ErrIncompleteChargeSchedule},
		{"unknown tier", "s", 2, with(func(t *[]schedule.TierCharge) { (*t)[0].Tier = "WEEKLY" }), 1, shared.ErrValidation},
		{"duplicate tier", "s", 2, with(func(t *[]schedule.TierCharge) { *t = append(*t, (*t)[0]) }), 1, shared.ErrValidation},
		{"negative charge", "s", 2, with(func(t *[]schedule.TierCharge) { (*t)[1].Charge = brl(-1) }), 1, shared.ErrNegativeAmount},
		{"charge currency", "s", 2, with(func(t *[]schedule.TierCharge) { (*t)[1].Charge.Currency = "USD" }), 1, shared.ErrCurrencyMismatch},
		{"negative free-over", "s", 2, with(func(t *[]schedule.TierCharge) { (*t)[0].FreeOver = brl(-1) }), 1, shared.ErrNegativeAmount},
		{"free-over currency", "s", 2, with(func(t *[]schedule.TierCharge) { (*t)[0].FreeOver.Currency = "USD" }), 1, shared.ErrCurrencyMismatch},
		{"same version", "s", 2, fullTiers(), 2, schedule.ErrNonIncreasingVersion},
		{"lower version", "s", 2, fullTiers(), 5, schedule.ErrNonIncreasingVersion},
	}
	for _, c := range cases {
		s, evs, err := schedule.Publish(c.id, c.version, c.tiers, c.current, cur, t0)
		if !errors.Is(err, c.want) || s != nil || evs != nil {
			t.Errorf("%s: Publish() = %v, %v, %v; want %v", c.name, s, evs, err, c.want)
		}
	}
	if _, _, err := schedule.Publish("s", 3, fullTiers(), 2, cur, t0); err != nil {
		t.Errorf("a version one above the current must be accepted: %v", err)
	}
	if _, _, err := schedule.Publish("s", shared.MaxInt32, fullTiers(), 0, cur, t0); err != nil {
		t.Errorf("the integer ceiling itself must be accepted: %v", err)
	}
	tiers := fullTiers()
	tiers[0].FreeOver = brl(0)
	if _, _, err := schedule.Publish("s", 1, tiers, 0, cur, t0); err != nil {
		t.Errorf("a free-over threshold of zero is a valid amount: %v", err)
	}
}

func TestSupersede(t *testing.T) {
	s := mustPublish(t, 1)
	if err := s.Supersede(); err != nil {
		t.Fatal(err)
	}
	if s.State() != schedule.StateSuperseded {
		t.Errorf("State() = %s", s.State())
	}
	if err := s.Supersede(); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("second Supersede() = %v, want ErrIllegalTransition", err)
	}
}

func TestChargeFor(t *testing.T) {
	s := mustPublish(t, 1)
	cases := []struct {
		tier  shared.Tier
		goods int64
		want  int64
	}{
		{shared.TierSameDay, 100000, 2990}, // no threshold: always charged
		{shared.TierNextDay, 29999, 1490},
		{shared.TierNextDay, 30000, 0}, // the threshold itself ships free
		{shared.TierNextDay, 30001, 0},
		{shared.TierTwoDay, 0, 990},
		{shared.TierStandard, 14999, 0}, // charge is 0 anyway
	}
	for _, c := range cases {
		got, err := s.ChargeFor(c.tier, brl(c.goods))
		if err != nil || got != brl(c.want) {
			t.Errorf("ChargeFor(%s, %d) = %v, %v; want %d", c.tier, c.goods, got, err, c.want)
		}
	}
	if _, err := s.ChargeFor("WEEKLY", brl(1)); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("ChargeFor(unknown tier) = %v, want ErrValidation", err)
	}
	// a threshold of zero makes the tier always free.
	tiers := fullTiers()
	tiers[0].FreeOver = brl(0)
	z, _, err := schedule.Publish("z", 1, tiers, 0, cur, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := z.ChargeFor(shared.TierSameDay, brl(0)); got != brl(2990) {
		t.Errorf("a tier without threshold must charge, got %v", got)
	}
	// the free charge keeps the currency.
	if got, _ := s.ChargeFor(shared.TierNextDay, brl(30000)); got.Currency != cur {
		t.Errorf("free charge currency = %q", got.Currency)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	s := mustPublish(t, 4)
	if err := s.Supersede(); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	snap.Tiers[0].Charge.AmountMinor = 1
	if s.Tiers()[0].Charge.AmountMinor == 1 {
		t.Error("Snapshot() shares its tier slice with the aggregate")
	}
	snap = s.Snapshot()
	r, err := schedule.Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Snapshot()
	if got.ID != snap.ID || got.Version != 4 || got.State != schedule.StateSuperseded || !got.PublishedAt.Equal(t0) || len(got.Tiers) != 4 {
		t.Errorf("round trip = %+v", got)
	}
	for i := range got.Tiers {
		if got.Tiers[i] != snap.Tiers[i] {
			t.Errorf("tier %d = %+v, want %+v", i, got.Tiers[i], snap.Tiers[i])
		}
	}
	// a snapshot in any tier order restores in speed order.
	snap.Tiers[0], snap.Tiers[3] = snap.Tiers[3], snap.Tiers[0]
	r, err = schedule.Restore(snap)
	if err != nil || r.Tiers()[0].Tier != shared.TierSameDay {
		t.Errorf("Restore(reordered) = %v, %v", r, err)
	}
	snap.PublishedAt = time.Date(2026, 10, 24, 9, 0, 0, 123456789, time.UTC)
	r, err = schedule.Restore(snap)
	if err != nil || r.PublishedAt().Nanosecond() != 123456000 {
		t.Errorf("Restore() kept sub-microsecond precision: %v %v", r, err)
	}
}

func TestRestoreRefusesCorruptSnapshots(t *testing.T) {
	good := func() schedule.Snapshot { return mustPublish(t, 2).Snapshot() }
	cases := map[string]func(*schedule.Snapshot){
		"bad id":          func(s *schedule.Snapshot) { s.ID = "bad id" },
		"zero version":    func(s *schedule.Snapshot) { s.Version = 0 },
		"huge version":    func(s *schedule.Snapshot) { s.Version = shared.MaxInt32 + 1 },
		"unknown state":   func(s *schedule.Snapshot) { s.State = "OLD" },
		"no published at": func(s *schedule.Snapshot) { s.PublishedAt = time.Time{} },
		"no tiers":        func(s *schedule.Snapshot) { s.Tiers = nil },
		"missing tier":    func(s *schedule.Snapshot) { s.Tiers = s.Tiers[:3] },
		"bad currency": func(s *schedule.Snapshot) {
			for i := range s.Tiers {
				s.Tiers[i].Charge.Currency = "br"
				s.Tiers[i].FreeOver = shared.Money{}
			}
		},
		"mixed currency": func(s *schedule.Snapshot) { s.Tiers[1].Charge.Currency = "USD" },
	}
	for name, mutate := range cases {
		s := good()
		mutate(&s)
		if _, err := schedule.Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: Restore() = %v, want ErrInvalidSnapshot", name, err)
		}
	}
	s := good()
	s.Version = shared.MaxInt32
	if _, err := schedule.Restore(s); err != nil {
		t.Errorf("Restore(max version) = %v", err)
	}
	if !schedule.StateCurrent.Valid() || !schedule.StateSuperseded.Valid() || schedule.State("").Valid() {
		t.Error("State.Valid() is wrong")
	}
}
