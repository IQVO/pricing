package schedule_test

import (
	"errors"
	"testing"

	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// consolidatedRow declares CONSOLIDATED; a freeOver of 0 means no threshold (the
// absent, zero Money).
func consolidatedRow(charge, freeOver int64) schedule.TierCharge {
	tc := schedule.TierCharge{Tier: shared.TierConsolidated, Charge: brl(charge)}
	if freeOver != 0 {
		tc.FreeOver = brl(freeOver)
	}
	return tc
}

func publishWith(t *testing.T, extra ...schedule.TierCharge) (*schedule.DeliveryChargeSchedule, []shared.Event) {
	t.Helper()
	s, evs, err := schedule.Publish("sch-c", 1, append(fullTiers(), extra...), 0, cur, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s, evs
}

// A schedule that does not name CONSOLIDATED stays exactly the four classic
// rows: no synthesised row in the aggregate, the snapshot or the event (ADR 0010).
func TestAScheduleWithoutConsolidatedKeepsItsFourRows(t *testing.T) {
	s, evs := publishWith(t)
	if got := s.Tiers(); len(got) != 4 || got[3].Tier != shared.TierStandard {
		t.Errorf("Tiers() = %v, want the four classic rows only", got)
	}
	if got := evs[0].(schedule.DeliveryChargeScheduleChanged).Tiers; len(got) != 4 {
		t.Errorf("event tiers = %v, want four", got)
	}
	if got := s.Snapshot().Tiers; len(got) != 4 {
		t.Errorf("snapshot tiers = %v, want four", got)
	}
}

func TestConsolidatedWithoutADeclaredRowIsFree(t *testing.T) {
	s, _ := publishWith(t)
	for _, goods := range []int64{0, 1, 29999, 1000000} {
		got, err := s.ChargeFor(shared.TierConsolidated, brl(goods))
		if err != nil || got != brl(0) {
			t.Errorf("ChargeFor(CONSOLIDATED, %d) = %v, %v; want a free %s charge", goods, got, err, cur)
		}
	}
	// the classic tiers are unchanged by the new tier.
	for tier, want := range map[shared.Tier]int64{shared.TierSameDay: 2990, shared.TierNextDay: 1490, shared.TierTwoDay: 990, shared.TierStandard: 0} {
		if got, err := s.ChargeFor(tier, brl(100)); err != nil || got != brl(want) {
			t.Errorf("ChargeFor(%s) = %v, %v; want %d", tier, got, err, want)
		}
	}
}

func TestTheFreeChargeTakesTheScheduleCurrency(t *testing.T) {
	usd := func(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: "USD"} }
	var tiers []schedule.TierCharge
	for _, tc := range fullTiers() {
		tiers = append(tiers, schedule.TierCharge{Tier: tc.Tier, Charge: usd(tc.Charge.AmountMinor)})
	}
	s, _, err := schedule.Publish("sch-usd", 1, tiers, 0, "USD", t0)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ChargeFor(shared.TierConsolidated, usd(500)); got != usd(0) {
		t.Errorf("free CONSOLIDATED charge = %v, want 0 USD", got)
	}
}

func TestADeclaredConsolidatedRowIsHonoured(t *testing.T) {
	s, evs := publishWith(t, consolidatedRow(490, 10000))
	for goods, want := range map[int64]int64{0: 490, 9999: 490, 10000: 0, 10001: 0} {
		if got, err := s.ChargeFor(shared.TierConsolidated, brl(goods)); err != nil || got != brl(want) {
			t.Errorf("ChargeFor(CONSOLIDATED, %d) = %v, %v; want %d", goods, got, err, want)
		}
	}
	rows := s.Tiers()
	if len(rows) != 5 || rows[4].Tier != shared.TierConsolidated || rows[4].Charge != brl(490) {
		t.Errorf("Tiers() = %v, want CONSOLIDATED as the fifth row", rows)
	}
	if got := evs[0].(schedule.DeliveryChargeScheduleChanged).Tiers; len(got) != 5 || got[4].Tier != shared.TierConsolidated {
		t.Errorf("event tiers = %v, want the declared CONSOLIDATED row last", got)
	}
}

func TestADeclaredConsolidatedRowWithoutAThresholdAlwaysCharges(t *testing.T) {
	s, _ := publishWith(t, consolidatedRow(490, 0))
	if got, _ := s.ChargeFor(shared.TierConsolidated, brl(1000000)); got != brl(490) {
		t.Errorf("charge = %v, want 490", got)
	}
}

func TestADeclaredConsolidatedRowMayBeZeroOrFirst(t *testing.T) {
	tiers := append([]schedule.TierCharge{consolidatedRow(0, 0)}, fullTiers()...)
	s, _, err := schedule.Publish("sch-c", 1, tiers, 0, cur, t0)
	if err != nil {
		t.Fatal(err)
	}
	if rows := s.Tiers(); len(rows) != 5 || rows[4].Tier != shared.TierConsolidated {
		t.Errorf("Tiers() = %v, want speed order with CONSOLIDATED last", rows)
	}
}

func TestConsolidatedDoesNotStandInForAClassicTier(t *testing.T) {
	tiers := fullTiers()[:3] // STANDARD missing
	tiers = append(tiers, consolidatedRow(0, 0))
	if _, _, err := schedule.Publish("s", 1, tiers, 0, cur, t0); !errors.Is(err, schedule.ErrIncompleteChargeSchedule) {
		t.Errorf("a schedule missing a classic tier = %v, want ErrIncompleteChargeSchedule", err)
	}
}

func TestConsolidatedRowRefusals(t *testing.T) {
	bad := consolidatedRow(-1, 0)
	badCurrency := consolidatedRow(1, 0)
	badCurrency.Charge.Currency = "USD"
	badFreeOver := consolidatedRow(1, 5)
	badFreeOver.FreeOver.Currency = "USD"
	cases := map[string]struct {
		extra []schedule.TierCharge
		want  error
	}{
		"twice":              {[]schedule.TierCharge{consolidatedRow(1, 0), consolidatedRow(2, 0)}, shared.ErrValidation},
		"negative charge":    {[]schedule.TierCharge{bad}, shared.ErrNegativeAmount},
		"charge currency":    {[]schedule.TierCharge{badCurrency}, shared.ErrCurrencyMismatch},
		"free-over currency": {[]schedule.TierCharge{badFreeOver}, shared.ErrCurrencyMismatch},
	}
	for name, c := range cases {
		if _, _, err := schedule.Publish("s", 1, append(fullTiers(), c.extra...), 0, cur, t0); !errors.Is(err, c.want) {
			t.Errorf("%s: Publish() = %v, want %v", name, err, c.want)
		}
	}
}

func TestUnknownTiersAreStillRefused(t *testing.T) {
	s, _ := publishWith(t, consolidatedRow(1, 0))
	for _, tier := range []shared.Tier{"", "consolidated", "WEEKLY", "OVERNIGHT"} {
		if _, err := s.ChargeFor(tier, brl(1)); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("ChargeFor(%q) = %v, want ErrValidation", tier, err)
		}
		bare, _ := publishWith(t)
		if _, err := bare.ChargeFor(tier, brl(1)); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("ChargeFor(%q) on a schedule without CONSOLIDATED = %v, want ErrValidation", tier, err)
		}
		if _, _, err := schedule.Publish("s", 1, append(fullTiers(), schedule.TierCharge{Tier: tier, Charge: brl(1)}), 0, cur, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("Publish naming tier %q = %v, want ErrValidation", tier, err)
		}
	}
}

func TestConsolidatedRowSurvivesSnapshotAndRestore(t *testing.T) {
	s, _ := publishWith(t, consolidatedRow(490, 10000))
	got, err := schedule.Restore(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if rows := got.Tiers(); len(rows) != 5 || rows[4] != consolidatedRow(490, 10000) {
		t.Errorf("restored Tiers() = %v", rows)
	}
	bare, _ := publishWith(t)
	again, err := schedule.Restore(bare.Snapshot())
	if err != nil || len(again.Tiers()) != 4 {
		t.Errorf("a schedule stored before CONSOLIDATED restores with four rows, got %v, %v", again.Tiers(), err)
	}
	if free, _ := again.ChargeFor(shared.TierConsolidated, brl(50)); free != brl(0) {
		t.Errorf("a restored schedule without the row still prices CONSOLIDATED free, got %v", free)
	}
}
