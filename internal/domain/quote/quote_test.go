package quote_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

const cur = "BRL"

var (
	t0  = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	ttl = 15 * time.Minute
)

func brl(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: cur} }

func sched(t *testing.T) *schedule.DeliveryChargeSchedule {
	t.Helper()
	s, _, err := schedule.Publish("sch-1", 7, []schedule.TierCharge{
		{Tier: shared.TierSameDay, Charge: brl(2990)},
		{Tier: shared.TierNextDay, Charge: brl(1490), FreeOver: brl(30000)},
		{Tier: shared.TierTwoDay, Charge: brl(990)},
		{Tier: shared.TierStandard, Charge: brl(500), FreeOver: brl(15000)},
	}, 0, cur, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func table(t *testing.T) *tax.TaxTable {
	t.Helper()
	tt, _, err := tax.Publish("tax-1", 3, []tax.TaxBand{
		{PostalPrefixFrom: "000", PostalPrefixTo: "499", RateBasisPoints: 1000},
		{PostalPrefixFrom: "500", PostalPrefixTo: "999", RateBasisPoints: 250},
	}, 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

func line(id string, qty int, unit int64) quote.LineInput {
	return quote.LineInput{CatalogItemID: id, Quantity: qty, Unit: brl(unit), Priced: true, PriceVersion: 4}
}

func input() quote.IssueInput {
	return quote.IssueInput{
		ID: "q-1", CartID: "cart-1", DestinationPostalPrefix: "100", Currency: cur, TTL: ttl, Now: t0,
		Lines: []quote.LineInput{line("a", 2, 5000), line("b", 1, 12345)},
		Groups: []quote.GroupInput{
			{GroupNo: 1, OfferID: "off-1", Tier: shared.TierNextDay, LineNos: []int{1}},
			{GroupNo: 2, OfferID: "off-2", Tier: shared.TierTwoDay, LineNos: []int{2}},
		},
	}
}

func mustIssue(t *testing.T, in quote.IssueInput) *quote.PriceQuote {
	t.Helper()
	q, _, err := quote.Issue(in, sched(t), table(t))
	if err != nil {
		t.Fatalf("Issue() = %v", err)
	}
	return q
}

func TestIssuePricesTheSessionExactly(t *testing.T) {
	q, evs, err := quote.Issue(input(), sched(t), table(t))
	if err != nil {
		t.Fatal(err)
	}
	// goods: 2*5000 + 12345 = 22345. group 1 (10000 goods, NEXT_DAY, free over 30000) 1490;
	// group 2 (12345, TWO_DAY, no threshold) 990. base 22345+1490+990 = 24825.
	// tax 10% = 2482.5 -> 2483 (half up). total 27308.
	verify(t, "totals", q, check{"tax", q.TaxMinor() == 2483}, check{"total", q.TotalMinor() == 27308})
	lines := q.Lines()
	assertIssuedLines(t, lines)
	groups := q.Groups()
	assertIssuedGroups(t, groups)
	assertIssuedHeader(t, q)
	verify(t, "timestamps", q, check{"issuedAt", q.IssuedAt().Equal(t0)}, check{"expiresAt", q.ExpiresAt().Equal(t0.Add(ttl))})
	q.MarkPersisted()
	if q.LoadedVersion() != 1 {
		t.Errorf("LoadedVersion() = %d after MarkPersisted", q.LoadedVersion())
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	e, ok := evs[0].(quote.PriceQuoteIssued)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	assertIssuedEvent(t, e)
	// the accessors return copies.
	lines[0].Quantity = 99
	groups[0].GroupNo = 99
	if q.Lines()[0].Quantity == 99 || q.Groups()[0].GroupNo == 99 {
		t.Error("Lines()/Groups() return the aggregate's own slices")
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

func assertIssuedLines(t *testing.T, lines []quote.Line) {
	t.Helper()
	if len(lines) != 2 {
		t.Fatalf("Lines() = %+v, want 2 lines", lines)
	}
	verify(t, "Lines()", lines,
		check{"lineNo 1", lines[0].LineNo == 1},
		check{"lineNo 2", lines[1].LineNo == 2},
		check{"line 1 total", lines[0].LineTotal == brl(10000)},
		check{"line 2 total", lines[1].LineTotal == brl(12345)},
		check{"unit price", lines[0].UnitPrice == brl(5000)},
		check{"quantity", lines[0].Quantity == 2},
		check{"catalog item", lines[0].CatalogItemID == "a"},
		check{"price version", lines[0].PriceVersion == 4},
	)
}

func assertIssuedGroups(t *testing.T, groups []quote.Group) {
	t.Helper()
	if len(groups) != 2 {
		t.Fatalf("Groups() = %+v, want 2 groups", groups)
	}
	verify(t, "Groups()", groups,
		check{"groupNo 1", groups[0].GroupNo == 1},
		check{"tier", groups[0].Tier == shared.TierNextDay},
		check{"charge 1", groups[0].DeliveryCharge == brl(1490)},
		check{"groupNo 2", groups[1].GroupNo == 2},
		check{"charge 2", groups[1].DeliveryCharge == brl(990)},
	)
}

func assertIssuedHeader(t *testing.T, q *quote.PriceQuote) {
	t.Helper()
	verify(t, "quote", q,
		check{"ID", q.ID() == "q-1"},
		check{"CartID", q.CartID() == "cart-1"},
		check{"State", q.State() == quote.StateIssued},
		check{"Currency", q.Currency() == cur},
		check{"ScheduleVersion", q.ScheduleVersion() == 7},
		check{"TaxTableVersion", q.TaxTableVersion() == 3},
		check{"Version", q.Version() == 1},
		check{"LoadedVersion", q.LoadedVersion() == 0},
	)
}

func assertIssuedEvent(t *testing.T, e quote.PriceQuoteIssued) {
	t.Helper()
	verify(t, "event", e,
		check{"QuoteID", e.QuoteID == "q-1"},
		check{"CartID", e.CartID == "cart-1"},
		check{"Currency", e.Currency == cur},
		check{"TotalMinor", e.TotalMinor == 27308},
		check{"ExpiresAt", e.ExpiresAt.Equal(t0.Add(ttl))},
		check{"SubjectID", e.SubjectID() == "q-1"},
		check{"AggregateVersion", e.AggregateVersion() == 1},
		check{"OccurredAt", e.OccurredAt().Equal(t0)},
		check{"EventName", e.EventName() == "PriceQuoteIssued"},
		check{"Entity", e.Entity() == "pricequote"},
	)
}

func TestFreeOverThresholdAppliesPerGroupGoods(t *testing.T) {
	in := input()
	in.Lines = []quote.LineInput{line("a", 1, 29999), line("b", 1, 30000)}
	in.Groups = []quote.GroupInput{
		{GroupNo: 1, OfferID: "o1", Tier: shared.TierNextDay, LineNos: []int{1}}, // 29999 < 30000: charged
		{GroupNo: 2, OfferID: "o2", Tier: shared.TierNextDay, LineNos: []int{2}}, // 30000 reaches it: free
	}
	q := mustIssue(t, in)
	g := q.Groups()
	if g[0].DeliveryCharge != brl(1490) || g[1].DeliveryCharge != brl(0) {
		t.Errorf("charges = %v and %v, want 1490 and 0", g[0].DeliveryCharge, g[1].DeliveryCharge)
	}
	// two lines in one group count together.
	in.Lines = []quote.LineInput{line("a", 1, 15000), line("b", 1, 15000)}
	in.Groups = []quote.GroupInput{{GroupNo: 1, OfferID: "o1", Tier: shared.TierNextDay, LineNos: []int{1, 2}}}
	if got := mustIssue(t, in).Groups()[0].DeliveryCharge; got != brl(0) {
		t.Errorf("group goods 30000 must ship free, got %v", got)
	}
}

func TestTaxUsesTheDestinationBandAndRoundsHalfUp(t *testing.T) {
	cases := []struct {
		prefix  string
		unit    int64
		wantTax int64
	}{
		{"100", 10000, 1000 + 99}, // 10% of 10000 goods + 990 delivery (TWO_DAY) = 10990 -> 1099
		{"499", 10000, 1099},
		{"500", 10000, 275}, // 2.5% of 10990 = 274.75 -> 275
		{"999", 1, 5},       // base 1 + 990 = 991 -> 2.5% = 24.775 -> 25 ... see below
		{"ZZZ", 10000, 0},   // outside every band
		{"000", 5, 100},     // base 995 -> 99.5 -> 100 (half up)
		{"000", 4, 99},      // base 994 -> 99.4 -> 99
	}
	for _, c := range cases {
		in := input()
		in.DestinationPostalPrefix = c.prefix
		in.Lines = []quote.LineInput{line("a", 1, c.unit)}
		in.Groups = []quote.GroupInput{{GroupNo: 1, OfferID: "o", Tier: shared.TierTwoDay, LineNos: []int{1}}}
		q := mustIssue(t, in)
		base := c.unit + 990
		if c.prefix == "999" {
			c.wantTax = (base*250 + 5000) / 10000
		}
		if q.TaxMinor() != c.wantTax || q.TotalMinor() != base+c.wantTax {
			t.Errorf("prefix %s unit %d: tax %d total %d, want %d and %d", c.prefix, c.unit, q.TaxMinor(), q.TotalMinor(), c.wantTax, base+c.wantTax)
		}
	}
}

func TestIssueRefusals(t *testing.T) {
	mut := func(f func(*quote.IssueInput)) quote.IssueInput { in := input(); f(&in); return in }
	manyLines := make([]quote.LineInput, quote.MaxLines+1)
	for i := range manyLines {
		manyLines[i] = line("i"+string(rune('a'+i/26))+string(rune('a'+i%26)), 1, 1)
	}
	manyGroups := make([]quote.GroupInput, quote.MaxGroups+1)
	for i := range manyGroups {
		manyGroups[i] = quote.GroupInput{GroupNo: i + 1, OfferID: "o", Tier: shared.TierStandard, LineNos: []int{1}}
	}
	cases := []struct {
		name string
		in   quote.IssueInput
		want error
	}{
		{"bad quote id", mut(func(in *quote.IssueInput) { in.ID = "bad id" }), shared.ErrValidation},
		{"bad cart id", mut(func(in *quote.IssueInput) { in.CartID = "" }), shared.ErrValidation},
		{"no lines", mut(func(in *quote.IssueInput) { in.Lines = nil }), quote.ErrEmptyQuote},
		{"too many lines", mut(func(in *quote.IssueInput) { in.Lines = manyLines }), shared.ErrValidation},
		{"no groups", mut(func(in *quote.IssueInput) { in.Groups = nil }), shared.ErrValidation},
		{"too many groups", mut(func(in *quote.IssueInput) { in.Groups = manyGroups }), shared.ErrValidation},
		{"bad destination", mut(func(in *quote.IssueInput) { in.DestinationPostalPrefix = "10" }), shared.ErrValidation},
		{"zero ttl", mut(func(in *quote.IssueInput) { in.TTL = 0 }), shared.ErrValidation},
		{"negative ttl", mut(func(in *quote.IssueInput) { in.TTL = -time.Second }), shared.ErrValidation},
		{"bad catalog id", mut(func(in *quote.IssueInput) { in.Lines[0].CatalogItemID = "a b" }), shared.ErrValidation},
		{"same item twice", mut(func(in *quote.IssueInput) { in.Lines[1].CatalogItemID = "a" }), shared.ErrValidation},
		{"zero quantity", mut(func(in *quote.IssueInput) { in.Lines[0].Quantity = 0 }), shared.ErrValidation},
		{"too many units", mut(func(in *quote.IssueInput) { in.Lines[0].Quantity = quote.MaxQuantity + 1 }), shared.ErrValidation},
		{"unpriced item", mut(func(in *quote.IssueInput) { in.Lines[1].Priced = false }), quote.ErrItemNotPriced},
		{"negative unit price", mut(func(in *quote.IssueInput) { in.Lines[0].Unit = brl(-1) }), shared.ErrNegativeAmount},
		{"other currency", mut(func(in *quote.IssueInput) { in.Lines[0].Unit.Currency = "USD" }), shared.ErrCurrencyMismatch},
		{"group number zero", mut(func(in *quote.IssueInput) { in.Groups[0].GroupNo = 0 }), shared.ErrValidation},
		{"group number above the ceiling", mut(func(in *quote.IssueInput) { in.Groups[0].GroupNo = shared.MaxInt32 + 1 }), shared.ErrValidation},
		{"group number twice", mut(func(in *quote.IssueInput) { in.Groups[1].GroupNo = 1 }), shared.ErrValidation},
		{"bad offer id", mut(func(in *quote.IssueInput) { in.Groups[0].OfferID = "" }), shared.ErrValidation},
		{"group without lines", mut(func(in *quote.IssueInput) { in.Groups[0].LineNos = nil }), shared.ErrValidation},
		{"line number zero", mut(func(in *quote.IssueInput) { in.Groups[0].LineNos = []int{0} }), shared.ErrValidation},
		{"line number beyond the lines", mut(func(in *quote.IssueInput) { in.Groups[0].LineNos = []int{3} }), shared.ErrValidation},
		{"line in two groups", mut(func(in *quote.IssueInput) { in.Groups[1].LineNos = []int{1} }), shared.ErrValidation},
		{"line twice in one group", mut(func(in *quote.IssueInput) { in.Groups[0].LineNos = []int{1, 1} }), shared.ErrValidation},
		{"line in no group", mut(func(in *quote.IssueInput) { in.Groups = in.Groups[:1] }), shared.ErrValidation},
		{"unknown tier", mut(func(in *quote.IssueInput) { in.Groups[0].Tier = "WEEKLY" }), shared.ErrValidation},
	}
	for _, c := range cases {
		q, evs, err := quote.Issue(c.in, sched(t), table(t))
		if !errors.Is(err, c.want) || q != nil || evs != nil {
			t.Errorf("%s: Issue() = %v, %v, %v; want %v", c.name, q, evs, err, c.want)
		}
	}
	if _, _, err := quote.Issue(input(), nil, table(t)); !errors.Is(err, quote.ErrNoChargeSchedule) {
		t.Errorf("without a schedule: %v", err)
	}
	if _, _, err := quote.Issue(input(), sched(t), nil); !errors.Is(err, quote.ErrNoTaxTable) {
		t.Errorf("without a tax table: %v", err)
	}
	// the bounds themselves are accepted.
	in := input()
	in.Lines = make([]quote.LineInput, quote.MaxLines)
	for i := range in.Lines {
		in.Lines[i] = line("i"+string(rune('a'+i/26))+string(rune('a'+i%26)), quote.MaxQuantity, shared.MaxMinor)
	}
	nos := make([]int, quote.MaxLines)
	for i := range nos {
		nos[i] = i + 1
	}
	in.Groups = []quote.GroupInput{{GroupNo: shared.MaxInt32, OfferID: "o", Tier: shared.TierStandard, LineNos: nos}}
	q := mustIssue(t, in)
	if want := int64(quote.MaxLines) * int64(quote.MaxQuantity) * int64(shared.MaxMinor); q.Lines()[0].LineTotal.AmountMinor*int64(quote.MaxLines) != want {
		t.Errorf("max quote goods = %d", want)
	}
	in = input()
	in.Groups = []quote.GroupInput{{GroupNo: 1, OfferID: "o", Tier: shared.TierStandard, LineNos: []int{1, 2}}}
	if _, _, err := quote.Issue(in, sched(t), table(t)); err != nil {
		t.Errorf("one group holding every line: %v", err)
	}
}

func TestExpire(t *testing.T) {
	q := mustIssue(t, input())
	expires := q.ExpiresAt()
	if q.ExpiredAt(expires.Add(-time.Nanosecond)) {
		t.Error("a quote is binding until expiresAt")
	}
	if !q.ExpiredAt(expires) || !q.ExpiredAt(expires.Add(time.Hour)) {
		t.Error("a quote is expired at and after expiresAt")
	}
	if err := q.Expire(expires.Add(-time.Nanosecond)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("Expire() while binding = %v, want ErrIllegalTransition", err)
	}
	if q.State() != quote.StateIssued || q.Version() != 1 {
		t.Error("a refused Expire changed the quote")
	}
	if err := q.Expire(expires); err != nil {
		t.Fatal(err)
	}
	if q.State() != quote.StateExpired || q.Version() != 2 {
		t.Errorf("state %s version %d", q.State(), q.Version())
	}
	if !q.ExpiredAt(t0) {
		t.Error("a stored EXPIRED quote is expired at any instant")
	}
	// expiring again is a no-op.
	if err := q.Expire(expires.Add(time.Hour)); err != nil || q.Version() != 2 {
		t.Errorf("second Expire() = %v, version %d", err, q.Version())
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	q := mustIssue(t, input())
	snap := q.Snapshot()
	snap.Lines[0].Quantity = 99
	snap.Groups[0].GroupNo = 99
	if q.Lines()[0].Quantity == 99 || q.Groups()[0].GroupNo == 99 {
		t.Error("Snapshot() shares its slices with the aggregate")
	}
	snap = q.Snapshot()
	r, err := quote.Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTripped(t, snap, r.Snapshot())
	verify(t, "versions", r, check{"Version", r.Version() == 1}, check{"LoadedVersion", r.LoadedVersion() == 1})
	assertExpiredRoundTrip(t, q)
	assertMicrosecondPrecision(t)
}

func assertRoundTripped(t *testing.T, snap, got quote.Snapshot) {
	t.Helper()
	verify(t, "round trip", got,
		check{"ID", got.ID == snap.ID},
		check{"CartID", got.CartID == "cart-1"},
		check{"State", got.State == quote.StateIssued},
		check{"Currency", got.Currency == cur},
		check{"TaxMinor", got.TaxMinor == 2483},
		check{"TotalMinor", got.TotalMinor == 27308},
		check{"ScheduleVersion", got.ScheduleVersion == 7},
		check{"TaxTableVersion", got.TaxTableVersion == 3},
		check{"Version", got.Version == 1},
		check{"IssuedAt", got.IssuedAt.Equal(t0)},
		check{"ExpiresAt", got.ExpiresAt.Equal(t0.Add(ttl))},
		check{"Lines", len(got.Lines) == 2},
		check{"Groups", len(got.Groups) == 2},
	)
}

// an expired quote round-trips at version 2.
func assertExpiredRoundTrip(t *testing.T, q *quote.PriceQuote) {
	t.Helper()
	if err := q.Expire(q.ExpiresAt()); err != nil {
		t.Fatal(err)
	}
	r, err := quote.Restore(q.Snapshot())
	if err != nil {
		t.Fatalf("Restore(expired) = %v", err)
	}
	verify(t, "Restore(expired)", r, check{"State", r.State() == quote.StateExpired}, check{"Version", r.Version() == 2})
}

func assertMicrosecondPrecision(t *testing.T) {
	t.Helper()
	snap := mustIssue(t, input()).Snapshot()
	snap.IssuedAt = time.Date(2026, 10, 24, 9, 0, 0, 123456789, time.UTC)
	snap.ExpiresAt = snap.IssuedAt.Add(ttl)
	r, err := quote.Restore(snap)
	if err != nil {
		t.Fatalf("Restore() = %v", err)
	}
	verify(t, "sub-microsecond precision", r,
		check{"IssuedAt", r.IssuedAt().Nanosecond() == 123456000},
		check{"ExpiresAt", r.ExpiresAt().Nanosecond() == 123456000},
	)
}

func TestRestoreRefusesCorruptSnapshots(t *testing.T) {
	good := func() quote.Snapshot { return mustIssue(t, input()).Snapshot() }
	cases := map[string]func(*quote.Snapshot){
		"bad id":                 func(s *quote.Snapshot) { s.ID = "bad id" },
		"bad cart id":            func(s *quote.Snapshot) { s.CartID = "" },
		"unknown state":          func(s *quote.Snapshot) { s.State = "GONE" },
		"bad currency":           func(s *quote.Snapshot) { s.Currency = "br" },
		"no schedule version":    func(s *quote.Snapshot) { s.ScheduleVersion = 0 },
		"no tax table version":   func(s *quote.Snapshot) { s.TaxTableVersion = 0 },
		"not issued":             func(s *quote.Snapshot) { s.IssuedAt = time.Time{} },
		"expires at issue":       func(s *quote.Snapshot) { s.ExpiresAt = s.IssuedAt },
		"issued with version 2":  func(s *quote.Snapshot) { s.Version = 2 },
		"expired with version 1": func(s *quote.Snapshot) { s.State = quote.StateExpired },
		"version 3":              func(s *quote.Snapshot) { s.State = quote.StateExpired; s.Version = 3 },
		"no lines":               func(s *quote.Snapshot) { s.Lines = nil },
		"too many lines":         func(s *quote.Snapshot) { s.Lines = make([]quote.Line, quote.MaxLines+1) },
		"line number":            func(s *quote.Snapshot) { s.Lines[1].LineNo = 5 },
		"line item id":           func(s *quote.Snapshot) { s.Lines[0].CatalogItemID = "a b" },
		"duplicate item":         func(s *quote.Snapshot) { s.Lines[1].CatalogItemID = "a" },
		"zero quantity":          func(s *quote.Snapshot) { s.Lines[0].Quantity = 0 },
		"too many units":         func(s *quote.Snapshot) { s.Lines[0].Quantity = quote.MaxQuantity + 1 },
		"no price version":       func(s *quote.Snapshot) { s.Lines[0].PriceVersion = 0 },
		"unit currency":          func(s *quote.Snapshot) { s.Lines[0].UnitPrice.Currency = "USD" },
		"line total":             func(s *quote.Snapshot) { s.Lines[0].LineTotal.AmountMinor++ },
		"no groups":              func(s *quote.Snapshot) { s.Groups = nil },
		"too many groups":        func(s *quote.Snapshot) { s.Groups = make([]quote.Group, quote.MaxGroups+1) },
		"group number zero":      func(s *quote.Snapshot) { s.Groups[0].GroupNo = 0 },
		"group number ceiling":   func(s *quote.Snapshot) { s.Groups[0].GroupNo = shared.MaxInt32 + 1 },
		"group number twice":     func(s *quote.Snapshot) { s.Groups[1].GroupNo = 1 },
		"group tier":             func(s *quote.Snapshot) { s.Groups[0].Tier = "WEEKLY" },
		"charge currency":        func(s *quote.Snapshot) { s.Groups[0].DeliveryCharge.Currency = "USD" },
		"negative tax":           func(s *quote.Snapshot) { s.TaxMinor = -1; s.TotalMinor -= s.TaxMinor + 1 + 2483 - 1 },
		"total mismatch":         func(s *quote.Snapshot) { s.TotalMinor++ },
	}
	for name, mutate := range cases {
		s := good()
		mutate(&s)
		if _, err := quote.Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: Restore() = %v, want ErrInvalidSnapshot", name, err)
		}
	}
	if !quote.StateIssued.Valid() || !quote.StateExpired.Valid() || quote.State("").Valid() {
		t.Error("State.Valid() is wrong")
	}
}
