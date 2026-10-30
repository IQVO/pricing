package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

var base = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

func brl(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: "BRL"} }

// harness builds the READ-ONLY Deps over the in-memory store and keeps the
// command use cases to itself: they only seed state through the real use
// cases, the adapter under test never receives them.
type harness struct {
	t     *testing.T
	store *memory.Store
	clock *memory.Clock
	all   usecases.Deps
	deps  Deps
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clock := memory.NewClock(base)
	s := memory.NewStore(clock)
	all := usecases.Deps{
		ItemPrices: s.ItemPrices(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Quotes: s.Quotes(),
		Catalog: s.Catalog(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "BRL",
	}
	return &harness{
		t: t, store: s, clock: clock, all: all,
		deps: Deps{GetItemPrice: usecases.GetItemPrice{Deps: all}, GetPriceQuote: usecases.GetPriceQuote{Deps: all}},
	}
}

func (h *harness) must(err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
}

// priced creates a catalogued item with the list price.
func (h *harness) priced(id string, list int64) {
	h.t.Helper()
	ctx := context.Background()
	h.must(usecases.Consumer{Deps: h.all}.CatalogItemCreated(ctx, "evt-"+id, "t", id))
	_, err := usecases.CreateItemPrice{Deps: h.all}.Execute(ctx, usecases.CreateItemPriceInput{CatalogItemID: id, ListPrice: brl(list)})
	h.must(err)
}

func (h *harness) saleRunningNow(id string, price int64) {
	h.t.Helper()
	_, err := usecases.ScheduleSale{Deps: h.all}.Execute(context.Background(), usecases.ScheduleSaleInput{
		CatalogItemID: id, Price: brl(price), From: base.Add(-time.Hour), To: base.Add(24 * time.Hour),
	})
	h.must(err)
}

// quoted publishes the policies and issues a quote for 2 x sku-1 (1000 each).
func (h *harness) quoted() string {
	h.t.Helper()
	ctx := context.Background()
	h.priced("sku-1", 1000)
	var tiers []schedule.TierCharge
	for _, tier := range shared.Tiers() {
		tiers = append(tiers, schedule.TierCharge{Tier: tier, Charge: brl(500)})
	}
	_, err := usecases.PublishChargeSchedule{Deps: h.all}.Execute(ctx, usecases.PublishChargeScheduleInput{Version: 1, Tiers: tiers})
	h.must(err)
	_, err = usecases.PublishTaxTable{Deps: h.all}.Execute(ctx, usecases.PublishTaxTableInput{Version: 1, Bands: []tax.TaxBand{{PostalPrefixFrom: "000", PostalPrefixTo: "999", RateBasisPoints: 1000}}})
	h.must(err)
	q, err := usecases.IssuePriceQuote{Deps: h.all}.Execute(ctx, usecases.IssuePriceQuoteInput{
		CartID: "cart-1", DestinationPostalPrefix: "010",
		Lines:  []usecases.QuoteLine{{CatalogItemID: "sku-1", Quantity: 2}},
		Groups: []usecases.QuoteGroup{{GroupNo: 1, OfferID: "off-1", Tier: shared.TierStandard, LineNos: []int{1}}},
	})
	h.must(err)
	return q.QuoteID
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  *struct {
		ItemPrice *struct {
			CatalogItemID  string                   `json:"catalogItemId"`
			State          string                   `json:"state"`
			ListPrice      wireMoney                `json:"listPrice"`
			EffectivePrice wireMoney                `json:"effectivePrice"`
			Sales          []map[string]interface{} `json:"sales"`
			Version        int64                    `json:"version"`
		} `json:"itemPrice"`
		PriceQuote *struct {
			QuoteID    string           `json:"quoteId"`
			CartID     string           `json:"cartId"`
			State      string           `json:"state"`
			Currency   string           `json:"currency"`
			Lines      []map[string]any `json:"lines"`
			Groups     []map[string]any `json:"groups"`
			TaxMinor   int64            `json:"taxMinor"`
			TotalMinor int64            `json:"totalMinor"`
			Versions   map[string]int64 `json:"versions"`
			IssuedAt   time.Time        `json:"issuedAt"`
			ExpiresAt  time.Time        `json:"expiresAt"`
		} `json:"priceQuote"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wireMoney struct {
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// rawCall sends one message part and returns the raw reply data and text.
func (w *wire) rawCall(t *testing.T, part *a2a.Part) (json.RawMessage, string) {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return raw, string(text)
}

// call sends one message part and returns the decoded envelope and the
// one-line text summary of the agent's reply.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string) {
	t.Helper()
	raw, text := w.rawCall(t, part)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	return env, text
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != problemTypeBase+"/"+slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
	if env.Data != nil {
		t.Fatalf("a problem answer must carry no data, got %+v", env.Data)
	}
}

func TestWireItemPriceReadsTheEffectivePrice(t *testing.T) {
	h := newHarness(t)
	h.priced("sku-1", 1000)
	h.saleRunningNow("sku-1", 800)
	w := newWire(t, h.deps)

	env, text := w.skill(t, skillItemPrice, map[string]any{"catalogItemId": "sku-1"})
	p := env.Data.ItemPrice
	if env.Problem != nil || p == nil || env.Skill != skillItemPrice {
		t.Fatalf("envelope = %+v", env)
	}
	if p.CatalogItemID != "sku-1" || p.State != "ACTIVE" || p.ListPrice != (wireMoney{1000, "BRL"}) || p.EffectivePrice != (wireMoney{800, "BRL"}) || len(p.Sales) != 1 {
		t.Fatalf("item price = %+v, want ACTIVE list 1000 effective 800 with one sale", p)
	}
	if text != "sku-1 ACTIVE: effective 800 BRL (list 1000 BRL, minor units), 1 sale window(s), version "+itoa(p.Version) {
		t.Fatalf("summary = %q", text)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestWireItemPriceOfAWithdrawnItemIsStillAnswered(t *testing.T) {
	h := newHarness(t)
	h.priced("sku-1", 1000)
	_, err := usecases.WithdrawPrice{Deps: h.all}.Execute(context.Background(), "sku-1")
	h.must(err)

	env, _ := newWire(t, h.deps).skill(t, skillItemPrice, map[string]any{"catalogItemId": "sku-1"})
	if env.Problem != nil || env.Data.ItemPrice == nil || env.Data.ItemPrice.State != "WITHDRAWN" {
		t.Fatalf("a withdrawn price is returned by GET too, got %+v", env)
	}
}

func TestWireQuoteStatusIssuedThenExpired(t *testing.T) {
	h := newHarness(t)
	id := h.quoted()
	w := newWire(t, h.deps)

	env, text := w.skill(t, skillQuoteStatus, map[string]any{"quoteId": id})
	q := env.Data.PriceQuote
	if env.Problem != nil || q == nil || q.State != "ISSUED" || q.CartID != "cart-1" || q.Currency != "BRL" || len(q.Lines) != 1 || len(q.Groups) != 1 {
		t.Fatalf("envelope = %+v", env)
	}
	assertQuoteBreakdown(t, id, text, env)

	h.clock.Advance(16 * time.Minute)
	env, _ = w.skill(t, skillQuoteStatus, map[string]any{"quoteId": id})
	if env.Data.PriceQuote == nil || env.Data.PriceQuote.State != "EXPIRED" {
		t.Fatalf("a quote past expiresAt must be EXPIRED even before the policy stored it, got %+v", env)
	}
}

// assertQuoteBreakdown checks the priced amounts, the versions used and the
// one-line summary of the quote h.quoted issued.
func assertQuoteBreakdown(t *testing.T, id, text string, env wireEnvelope) {
	t.Helper()
	q := env.Data.PriceQuote
	// 2 x 1000 goods + 500 delivery, plus 10% tax: strictly more than the goods and delivery.
	if q.TotalMinor <= 2500 || q.TaxMinor <= 0 || q.Versions["chargeSchedule"] != 1 || q.Versions["taxTable"] != 1 || q.Versions["price:sku-1"] == 0 {
		t.Fatalf("quote = %+v", q)
	}
	if !strings.HasPrefix(text, "quote "+id+" ISSUED: total ") || !strings.Contains(text, "expires "+q.ExpiresAt.Format(time.RFC3339)) {
		t.Fatalf("summary = %q", text)
	}
}

func TestWireNotFoundIsAnsweredNotRaised(t *testing.T) {
	w := newWire(t, newHarness(t).deps)

	env, _ := w.skill(t, skillItemPrice, map[string]any{"catalogItemId": "ghost"})
	wantProblem(t, env, 404, "not-found")
	env, _ = w.skill(t, skillQuoteStatus, map[string]any{"quoteId": "ghost"})
	wantProblem(t, env, 404, "not-found")

	// An identifier longer than the REST path bound can never name a resource:
	// the REST adapter answers 404 for it and so does this one, without
	// reaching the use case or echoing the identifier.
	long := strings.Repeat("x", maxIDLength+1)
	env, _ = w.skill(t, skillItemPrice, map[string]any{"catalogItemId": long})
	wantProblem(t, env, 404, "not-found")
	if strings.Contains(env.Problem.Detail, long) {
		t.Fatal("the problem echoes the oversized identifier")
	}
	env, _ = w.skill(t, skillQuoteStatus, map[string]any{"quoteId": long})
	wantProblem(t, env, 404, "not-found")
}

// strictItemPrices and strictQuotes fail the way Postgres would on an
// identifier the REST adapter never lets through: text cannot hold U+0000
// (SQLSTATE 22021, a 500 if it ever reaches the database), and REST answers 404
// before the use case for an identifier past its path bound.
type strictItemPrices struct {
	ports.ItemPriceRepository
	// reached collects the identifiers that got past the adapter.
	reached *[]string
}

func (r strictItemPrices) Get(ctx context.Context, id itemprice.ID) (*itemprice.ItemPrice, error) {
	*r.reached = append(*r.reached, string(id))
	if err := unnameable(string(id)); err != nil {
		return nil, err
	}
	return r.ItemPriceRepository.Get(ctx, id)
}

type strictQuotes struct{ ports.QuoteRepository }

func (r strictQuotes) Get(ctx context.Context, id quote.ID) (*quote.PriceQuote, error) {
	if err := unnameable(string(id)); err != nil {
		return nil, err
	}
	return r.QuoteRepository.Get(ctx, id)
}

func unnameable(id string) error {
	if strings.ContainsRune(id, 0) {
		return errors.New("ERROR: invalid byte sequence for encoding \"UTF8\": 0x00 (SQLSTATE 22021)")
	}
	if len(id) > maxIDLength {
		return errors.New("an identifier past the REST path bound reached the repository")
	}
	return nil
}

// TestWireUnnameableIdentifiersNeverReachTheRepository: the REST adapter
// answers 404 for a path identifier carrying NUL or past 128 bytes, before any
// use case. The A2A skills answer the same 404 and never let such an
// identifier through (a NUL that reached Postgres would be a 500).
func TestWireUnnameableIdentifiersNeverReachTheRepository(t *testing.T) {
	h := newHarness(t)
	strict := h.all
	var reached []string
	strict.ItemPrices, strict.Quotes = strictItemPrices{h.all.ItemPrices, &reached}, strictQuotes{h.all.Quotes}
	w := newWire(t, Deps{GetItemPrice: usecases.GetItemPrice{Deps: strict}, GetPriceQuote: usecases.GetPriceQuote{Deps: strict}})

	for name, id := range map[string]string{"NUL": "sku\x00-1", "only NUL": "\x00", "past the bound": strings.Repeat("x", maxIDLength+1)} {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, skillItemPrice, map[string]any{"catalogItemId": id})
			wantProblem(t, env, 404, "not-found")
			env, _ = w.skill(t, skillQuoteStatus, map[string]any{"quoteId": id})
			wantProblem(t, env, 404, "not-found")
		})
	}
	if len(reached) != 0 {
		t.Fatalf("identifiers %q reached the repository", reached)
	}

	// ...while an identifier exactly at the REST bound is a normal lookup (the
	// domain only ever stores shorter ones, so it is simply not found).
	atBound := strings.Repeat("x", maxIDLength)
	env, _ := w.skill(t, skillItemPrice, map[string]any{"catalogItemId": atBound})
	wantProblem(t, env, 404, "not-found")
	if len(reached) != 1 || reached[0] != atBound {
		t.Fatalf("an identifier of exactly %d bytes must be looked up, repository saw %q", maxIDLength, reached)
	}
}

// failingItemPrices is an ItemPriceRepository whose reads fail with a message
// that must never reach the caller.
type failingItemPrices struct{ ports.ItemPriceRepository }

var errBoom = errors.New("pq: connection to postgres://pricing:hunter2@db.internal:5432 refused")

func (failingItemPrices) Get(context.Context, itemprice.ID) (*itemprice.ItemPrice, error) {
	return nil, errBoom
}

// TestWireInternalFailureIsAProblemThatDoesNotLeak: an unclassified error is a
// 500 problem, like REST, whose detail never carries the internal message.
func TestWireInternalFailureIsAProblemThatDoesNotLeak(t *testing.T) {
	h := newHarness(t)
	broken := h.all
	broken.ItemPrices = failingItemPrices{h.all.ItemPrices}
	w := newWire(t, Deps{GetItemPrice: usecases.GetItemPrice{Deps: broken}, GetPriceQuote: h.deps.GetPriceQuote})

	env, text := w.skill(t, skillItemPrice, map[string]any{"catalogItemId": "sku-1"})
	wantProblem(t, env, 500, "internal-error")
	for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
		if strings.Contains(s, "hunter2") || strings.Contains(s, "postgres://") {
			t.Fatalf("an answer leaks the internal error: %q", s)
		}
	}
}

func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, newHarness(t).deps)
	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"item: missing catalogItemId":   {skillItemPrice, map[string]any{}},
		"item: blank catalogItemId":     {skillItemPrice, map[string]any{"catalogItemId": "  "}},
		"item: unknown argument":        {skillItemPrice, map[string]any{"catalogItemId": "sku-1", "includeCost": true}},
		"item: typo'd argument":         {skillItemPrice, map[string]any{"catalogItemID2": "sku-1"}},
		"item: wrong type":              {skillItemPrice, map[string]any{"catalogItemId": 42}},
		"item: quote argument":          {skillItemPrice, map[string]any{"quoteId": "q1"}},
		"quote: missing quoteId":        {skillQuoteStatus, map[string]any{}},
		"quote: blank quoteId":          {skillQuoteStatus, map[string]any{"quoteId": ""}},
		"quote: unknown argument":       {skillQuoteStatus, map[string]any{"quoteId": "q1", "margin": true}},
		"quote: wrong type":             {skillQuoteStatus, map[string]any{"quoteId": []string{"q1"}}},
		"quote: item argument":          {skillQuoteStatus, map[string]any{"catalogItemId": "sku-1"}},
		"item: arguments not an object": {skillItemPrice, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var env wireEnvelope
			if tc.args == nil {
				// arguments given as a JSON string instead of an object
				env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": tc.skill, "arguments": "sku-1"}))
			} else {
				env, _ = w.skill(t, tc.skill, tc.args)
			}
			wantProblem(t, env, 400, "invalid-argument")
		})
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	h := newHarness(t)
	h.priced("sku-1", 1000)
	w := newWire(t, h.deps)

	env, _ := w.call(t, a2a.NewTextPart("not json at all"))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"item_price","arguments":{"catalogItemId":"sku-1"}} {"trailing":true}`))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": skillItemPrice, "arguments": map[string]any{"catalogItemId": "sku-1"}, "extra": 1}))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.skill(t, "launch_rocket", map[string]any{})
	wantProblem(t, env, 400, "unknown-skill")

	// Every state-changing REST operation is NOT a skill here.
	for _, write := range []string{"set_list_price", "schedule_sale", "cancel_sale", "withdraw_price", "issue_price_quote", "publish_charge_schedule", "publish_tax_table"} {
		env, _ = w.skill(t, write, map[string]any{})
		wantProblem(t, env, 400, "unknown-skill")
	}

	// A text part holding the same JSON is accepted (documented contract).
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"item_price","arguments":{"catalogItemId":"sku-1"}}`))
	if env.Problem != nil || env.Data.ItemPrice == nil {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %+v", env)
	}
}

// TestSkillsNeverWrite proves the agent is read-only end to end: after every
// kind of call, success or rejection, the store holds exactly what it held
// before (no row, no version bump, no outbox event).
func TestSkillsNeverWrite(t *testing.T) {
	h := newHarness(t)
	id := h.quoted()
	h.saleRunningNow("sku-1", 900)
	before, eventsBefore := h.store.Counts(), len(h.store.Events())
	w := newWire(t, h.deps)

	w.skill(t, skillItemPrice, map[string]any{"catalogItemId": "sku-1"})
	w.skill(t, skillItemPrice, map[string]any{"catalogItemId": "ghost"})
	w.skill(t, skillQuoteStatus, map[string]any{"quoteId": id})
	h.clock.Advance(time.Hour) // the quote is now EXPIRED: still not stored by a read
	w.skill(t, skillQuoteStatus, map[string]any{"quoteId": id})
	w.skill(t, skillQuoteStatus, map[string]any{"quoteId": "ghost"})
	w.skill(t, "set_list_price", map[string]any{"catalogItemId": "sku-1"})

	if after := h.store.Counts(); after != before {
		t.Fatalf("store changed by read skills: before %+v after %+v", before, after)
	}
	if got := len(h.store.Events()); got != eventsBefore {
		t.Fatalf("outbox events %d -> %d: a read skill must publish nothing", eventsBefore, got)
	}
}

// jsonKeys returns the sorted JSON object keys of v marshalled.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestAnswersExposeOnlyWhatRESTExposes pins the exact field set of both
// answers to the field set of the REST GET responses (apis/openapi.yaml
// ItemPrice and PriceQuote). A field added to a DTO here, such as an internal
// cost or margin, fails this test until the REST contract exposes it too.
func TestAnswersExposeOnlyWhatRESTExposes(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want []string
	}{
		{"item price", itemPriceDTO{}, []string{"catalogItemId", "effectivePrice", "listPrice", "sales", "state", "version"}},
		{"sale window", saleWindowDTO{}, []string{"from", "price", "saleId", "to"}},
		{"price quote", priceQuoteDTO{}, []string{"cartId", "currency", "expiresAt", "groups", "issuedAt", "lines", "quoteId", "state", "taxMinor", "totalMinor", "versions"}},
		{"quote line", quoteLineDTO{}, []string{"catalogItemId", "lineTotal", "quantity", "unitPrice"}},
		{"quote group", quoteGroupDTO{}, []string{"deliveryCharge", "groupNo", "tier"}},
		{"money", moneyDTO{}, []string{"amountMinor", "currency"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jsonKeys(t, tc.got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s answer fields = %v, want exactly the REST fields %v", tc.name, got, tc.want)
			}
		})
	}
	if got := jsonKeys(t, skillData{ItemPrice: &itemPriceDTO{}, PriceQuote: &priceQuoteDTO{}}); !reflect.DeepEqual(got, []string{"itemPrice", "priceQuote"}) {
		t.Fatalf("skill data fields = %v", got)
	}
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	h := newHarness(t)
	h.priced("sku-1", 1000)
	mux := http.NewServeMux()
	mux.Handle("/", Handler(NewExecutor(h.deps)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(Card(Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	w := &wire{client: client}
	env, _ := w.skill(t, skillItemPrice, map[string]any{"catalogItemId": "sku-1"})
	if env.Data == nil || env.Data.ItemPrice == nil {
		t.Fatalf("a client built from the served card could not call the agent: %+v", env)
	}
}
