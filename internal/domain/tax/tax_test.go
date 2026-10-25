package tax_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

func band(from, to string, bps int) tax.TaxBand {
	return tax.TaxBand{PostalPrefixFrom: from, PostalPrefixTo: to, RateBasisPoints: bps}
}

func bands() []tax.TaxBand {
	return []tax.TaxBand{band("200", "299", 1000), band("000", "099", 500), band("100", "1ZZ", 0)}
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

func mustPublish(t *testing.T, version int64) *tax.TaxTable {
	t.Helper()
	tt, _, err := tax.Publish("tax-1", version, bands(), 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

func TestPublishCreatesTheCurrentTableAndRaisesTheEvent(t *testing.T) {
	tt, evs, err := tax.Publish("tax-1", 3, bands(), 2, t0.Add(5*time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	verify(t, "table", tt,
		check{"ID", tt.ID() == "tax-1"},
		check{"Version", tt.Version() == 3},
		check{"State", tt.State() == tax.StateCurrent},
		check{"PublishedAt", tt.PublishedAt().Equal(t0)},
	)
	got := tt.Bands()
	verify(t, "Bands() lowest first", got,
		check{"count", len(got) == 3},
		check{"first", got[0].PostalPrefixFrom == "000"},
		check{"second", got[1].PostalPrefixFrom == "100"},
		check{"third", got[2].PostalPrefixFrom == "200"},
	)
	got[0].RateBasisPoints = 9
	if tt.Bands()[0].RateBasisPoints == 9 {
		t.Error("Bands() returns the aggregate's own slice")
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	e, ok := evs[0].(tax.TaxTableChanged)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	verify(t, "event", e,
		check{"TaxTableID", e.TaxTableID == "tax-1"},
		check{"SubjectID", e.SubjectID() == "tax-1"},
		check{"AggregateVersion", e.AggregateVersion() == 3},
		check{"OccurredAt", e.OccurredAt().Equal(t0)},
		check{"EventName", e.EventName() == "TaxTableChanged"},
		check{"Entity", e.Entity() == "taxtable"},
		check{"Bands", len(e.Bands) == 3},
	)
	e.Bands[0].RateBasisPoints = 9
	if tt.Bands()[0].RateBasisPoints == 9 {
		t.Error("the event shares its bands with the aggregate")
	}
}

func TestPublishRefusals(t *testing.T) {
	many := make([]tax.TaxBand, tax.MaxBands+1)
	for i := range many {
		many[i] = band(prefixOf(i), prefixOf(i), 100)
	}
	cases := []struct {
		name    string
		id      tax.ID
		version int64
		bands   []tax.TaxBand
		current int64
		want    error
	}{
		{"bad id", "bad id", 2, bands(), 1, shared.ErrValidation},
		{"version zero", "t", 0, bands(), 0, shared.ErrValidation},
		{"version above the integer ceiling", "t", shared.MaxInt32 + 1, bands(), 0, shared.ErrValidation},
		{"no bands", "t", 2, nil, 1, shared.ErrValidation},
		{"too many bands", "t", 2, many, 1, shared.ErrValidation},
		{"short prefix", "t", 2, []tax.TaxBand{band("00", "099", 1)}, 1, shared.ErrValidation},
		{"long prefix", "t", 2, []tax.TaxBand{band("000", "0999", 1)}, 1, shared.ErrValidation},
		{"lower-case prefix", "t", 2, []tax.TaxBand{band("000", "09a", 1)}, 1, shared.ErrValidation},
		{"symbol in prefix", "t", 2, []tax.TaxBand{band("0-0", "099", 1)}, 1, shared.ErrValidation},
		{"band ends before it starts", "t", 2, []tax.TaxBand{band("200", "100", 1)}, 1, shared.ErrValidation},
		{"negative rate", "t", 2, []tax.TaxBand{band("000", "099", -1)}, 1, tax.ErrRateOutOfRange},
		{"rate above the range", "t", 2, []tax.TaxBand{band("000", "099", tax.MaxRateBasisPoints+1)}, 1, tax.ErrRateOutOfRange},
		{"overlapping bands", "t", 2, []tax.TaxBand{band("000", "100", 1), band("100", "199", 1)}, 1, tax.ErrOverlappingTaxBand},
		{"nested bands", "t", 2, []tax.TaxBand{band("000", "500", 1), band("100", "199", 1)}, 1, tax.ErrOverlappingTaxBand},
		{"identical bands", "t", 2, []tax.TaxBand{band("100", "199", 1), band("100", "199", 1)}, 1, tax.ErrOverlappingTaxBand},
		{"same version", "t", 2, bands(), 2, tax.ErrNonIncreasingVersion},
		{"lower version", "t", 2, bands(), 9, tax.ErrNonIncreasingVersion},
	}
	for _, c := range cases {
		tt, evs, err := tax.Publish(c.id, c.version, c.bands, c.current, t0)
		if !errors.Is(err, c.want) || tt != nil || evs != nil {
			t.Errorf("%s: Publish() = %v, %v, %v; want %v", c.name, tt, evs, err, c.want)
		}
	}
	for name, b := range map[string][]tax.TaxBand{
		"adjacent bands":     {band("000", "099", 1), band("100", "199", 1)},
		"single-prefix band": {band("ABC", "ABC", 1)},
		"maximum rate":       {band("000", "099", tax.MaxRateBasisPoints)},
		"zero rate":          {band("000", "099", 0)},
	} {
		if _, _, err := tax.Publish("t", 1, b, 0, t0); err != nil {
			t.Errorf("%s: Publish() = %v", name, err)
		}
	}
	if _, _, err := tax.Publish("t", 3, bands(), 2, t0); err != nil {
		t.Errorf("a version one above the current must be accepted: %v", err)
	}
	if _, _, err := tax.Publish("t", shared.MaxInt32, bands(), 0, t0); err != nil {
		t.Errorf("the integer ceiling itself must be accepted: %v", err)
	}
	max := make([]tax.TaxBand, tax.MaxBands)
	for i := range max {
		max[i] = band(prefixOf(i), prefixOf(i), 100)
	}
	if _, _, err := tax.Publish("t", 1, max, 0, t0); err != nil {
		t.Errorf("exactly MaxBands bands must be accepted: %v", err)
	}
}

func prefixOf(i int) string {
	const digits = "0123456789"
	return string([]byte{digits[(i/100)%10], digits[(i/10)%10], digits[i%10]})
}

func TestSupersede(t *testing.T) {
	tt := mustPublish(t, 1)
	if err := tt.Supersede(); err != nil {
		t.Fatal(err)
	}
	if tt.State() != tax.StateSuperseded {
		t.Errorf("State() = %s", tt.State())
	}
	if err := tt.Supersede(); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("second Supersede() = %v, want ErrIllegalTransition", err)
	}
}

func TestRateFor(t *testing.T) {
	tt := mustPublish(t, 1)
	cases := map[string]int{
		"000": 500, "050": 500, "099": 500, // first band, both ends inclusive
		"100": 0, "1AA": 0, "1ZZ": 0,
		"200": 1000, "250": 1000, "299": 1000,
		"300": 0, // outside every band: untaxed
		"0AA": 0, "": 0,
	}
	for prefix, want := range cases {
		if got := tt.RateFor(prefix); got != want {
			t.Errorf("RateFor(%q) = %d, want %d", prefix, got, want)
		}
	}
	// a rate of zero inside a band is not the same as no band, but both tax nothing.
	if got := tt.RateFor("1M0"); got != 0 {
		t.Errorf("RateFor(1M0) = %d", got)
	}
}

func TestValidPrefix(t *testing.T) {
	for p, want := range map[string]bool{
		"000": true, "ABC": true, "9Z0": true, "A": false, "AB": false, "ABCD": false, "": false,
		"abc": false, "A-C": false, "A C": false, "@AB": false, "[AB": false, "/00": false, ":00": false, "0A:": false,
	} {
		if got := tax.ValidPrefix(p); got != want {
			t.Errorf("ValidPrefix(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	tt := mustPublish(t, 4)
	if err := tt.Supersede(); err != nil {
		t.Fatal(err)
	}
	snap := tt.Snapshot()
	snap.Bands[0].RateBasisPoints = 9
	if tt.Bands()[0].RateBasisPoints == 9 {
		t.Error("Snapshot() shares its band slice with the aggregate")
	}
	snap = tt.Snapshot()
	r, err := tax.Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Snapshot()
	if got.ID != "tax-1" || got.Version != 4 || got.State != tax.StateSuperseded || !got.PublishedAt.Equal(t0) || len(got.Bands) != 3 {
		t.Errorf("round trip = %+v", got)
	}
	for i := range got.Bands {
		if got.Bands[i] != snap.Bands[i] {
			t.Errorf("band %d = %+v, want %+v", i, got.Bands[i], snap.Bands[i])
		}
	}
	snap.PublishedAt = time.Date(2026, 10, 24, 9, 0, 0, 123456789, time.UTC)
	r, err = tax.Restore(snap)
	if err != nil || r.PublishedAt().Nanosecond() != 123456000 {
		t.Errorf("Restore() kept sub-microsecond precision: %v %v", r, err)
	}
}

func TestRestoreRefusesCorruptSnapshots(t *testing.T) {
	good := func() tax.Snapshot { return mustPublish(t, 2).Snapshot() }
	cases := map[string]func(*tax.Snapshot){
		"bad id":          func(s *tax.Snapshot) { s.ID = "bad id" },
		"zero version":    func(s *tax.Snapshot) { s.Version = 0 },
		"huge version":    func(s *tax.Snapshot) { s.Version = shared.MaxInt32 + 1 },
		"unknown state":   func(s *tax.Snapshot) { s.State = "OLD" },
		"no published at": func(s *tax.Snapshot) { s.PublishedAt = time.Time{} },
		"no bands":        func(s *tax.Snapshot) { s.Bands = nil },
		"overlap":         func(s *tax.Snapshot) { s.Bands[1].PostalPrefixFrom = "000" },
		"bad rate":        func(s *tax.Snapshot) { s.Bands[0].RateBasisPoints = -1 },
	}
	for name, mutate := range cases {
		s := good()
		mutate(&s)
		if _, err := tax.Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: Restore() = %v, want ErrInvalidSnapshot", name, err)
		}
	}
	s := good()
	s.Version = shared.MaxInt32
	if _, err := tax.Restore(s); err != nil {
		t.Errorf("Restore(max version) = %v", err)
	}
	if !tax.StateCurrent.Valid() || !tax.StateSuperseded.Valid() || tax.State("").Valid() {
		t.Error("State.Valid() is wrong")
	}
}
