package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
)

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%#v is not an object", v)
	}
	return m
}

func amount(t *testing.T, money any) float64 {
	t.Helper()
	return asMap(t, money)["amountMinor"].(float64)
}

func TestGetItemPriceReadsTheEffectivePrice(t *testing.T) {
	h := newHarness(t)
	h.priced("sku-1", 1000)
	h.saleRunningNow("sku-1", 800)

	got := h.ok("get_item_price", map[string]any{"catalogItemId": "sku-1"})
	if got["catalogItemId"] != "sku-1" || got["state"] != "ACTIVE" {
		t.Fatalf("item price = %v, want ACTIVE sku-1", got)
	}
	if amount(t, got["listPrice"]) != 1000 || amount(t, got["effectivePrice"]) != 800 || asMap(t, got["listPrice"])["currency"] != "BRL" {
		t.Fatalf("item price = %v, want list 1000 BRL and effective 800", got)
	}
	if sales, _ := got["sales"].([]any); len(sales) != 1 {
		t.Fatalf("sales = %v, want the one running window", got["sales"])
	}
}

func TestGetItemPriceOfAWithdrawnItemIsStillAnswered(t *testing.T) {
	h := newHarness(t)
	h.priced("sku-1", 1000)
	_, err := usecases.WithdrawPrice{Deps: h.all}.Execute(context.Background(), "sku-1")
	h.must(err)

	if got := h.ok("get_item_price", map[string]any{"catalogItemId": "sku-1"}); got["state"] != "WITHDRAWN" {
		t.Fatalf("a withdrawn price is returned by the REST GET too, got %v", got)
	}
}

func TestGetPriceQuoteIssuedThenExpired(t *testing.T) {
	h := newHarness(t)
	id := h.quoted()

	q := h.ok("get_price_quote", map[string]any{"quoteId": id})
	if q["quoteId"] != id || q["state"] != "ISSUED" || q["cartId"] != "cart-1" || q["currency"] != "BRL" {
		t.Fatalf("quote = %v", q)
	}
	if lines, _ := q["lines"].([]any); len(lines) != 1 {
		t.Fatalf("lines = %v, want one", q["lines"])
	}
	if groups, _ := q["groups"].([]any); len(groups) != 1 {
		t.Fatalf("groups = %v, want one", q["groups"])
	}
	versions := asMap(t, q["versions"])
	// 2 x 1000 goods + 500 delivery, plus 10% tax: strictly more than goods and delivery.
	if q["totalMinor"].(float64) <= 2500 || q["taxMinor"].(float64) <= 0 || versions["chargeSchedule"] != float64(1) || versions["taxTable"] != float64(1) {
		t.Fatalf("quote = %v", q)
	}

	h.clock.Advance(16 * time.Minute)
	if got := h.ok("get_price_quote", map[string]any{"quoteId": id}); got["state"] != "EXPIRED" {
		t.Fatalf("a quote past expiresAt must be EXPIRED even before the policy stored it, got %v", got["state"])
	}
}

func TestToolsAnswerNotFoundWithTheRESTSlug(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_item_price", map[string]any{"catalogItemId": "ghost"}, "not-found")
	failWith(t, h.session, "get_price_quote", map[string]any{"quoteId": "ghost"}, "not-found")

	// An identifier longer than the REST path bound can never name a resource:
	// REST answers 404 for it and so does this adapter, without echoing it.
	long := strings.Repeat("x", 129)
	res := call(t, h.session, "get_item_price", map[string]any{"catalogItemId": long})
	if !res.IsError || !strings.HasPrefix(text(res), "not-found: ") || strings.Contains(text(res), long) {
		t.Fatalf("oversized id: isError=%v text=%q, want a not-found that does not echo the id", res.IsError, text(res))
	}
	failWith(t, h.session, "get_price_quote", map[string]any{"quoteId": long}, "not-found")
}

func TestToolsRejectABlankIdentifier(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_item_price", map[string]any{"catalogItemId": "   "}, "validation-failed")
	failWith(t, h.session, "get_price_quote", map[string]any{"quoteId": ""}, "validation-failed")
}

// strictItemPrices and strictQuotes fail the way Postgres would on an
// identifier the REST adapter never lets through: text cannot hold U+0000
// (SQLSTATE 22021, a 500 if it ever reaches the database), and REST answers
// 404 before the use case for an identifier past its path bound.
type strictItemPrices struct {
	ports.ItemPriceRepository
	reached *[]string
}

func (r strictItemPrices) Get(ctx context.Context, id itemprice.ID) (*itemprice.ItemPrice, error) {
	*r.reached = append(*r.reached, string(id))
	if err := unnameable(string(id)); err != nil {
		return nil, err
	}
	return r.ItemPriceRepository.Get(ctx, id)
}

type strictQuotes struct {
	ports.QuoteRepository
	reached *[]string
}

func (r strictQuotes) Get(ctx context.Context, id quote.ID) (*quote.PriceQuote, error) {
	*r.reached = append(*r.reached, string(id))
	if err := unnameable(string(id)); err != nil {
		return nil, err
	}
	return r.QuoteRepository.Get(ctx, id)
}

func unnameable(id string) error {
	if strings.ContainsRune(id, 0) {
		return errors.New("ERROR: invalid byte sequence for encoding \"UTF8\": 0x00 (SQLSTATE 22021)")
	}
	if len(id) > 128 {
		return errors.New("an identifier past the REST path bound reached the repository")
	}
	return nil
}

// TestUnnameableIdentifiersNeverReachTheRepository: the REST adapter answers
// 404 for a path identifier carrying NUL or past 128 bytes, before any use
// case. The tools answer the same not-found and never let such an identifier
// through (a NUL that reached Postgres would be a 500).
func TestUnnameableIdentifiersNeverReachTheRepository(t *testing.T) {
	h := newHarness(t)
	var reached []string
	strict := h.all
	strict.ItemPrices, strict.Quotes = strictItemPrices{h.all.ItemPrices, &reached}, strictQuotes{h.all.Quotes, &reached}
	session := connectSession(t, readDeps(strict))

	for name, id := range map[string]string{"NUL": "sku\x00-1", "only NUL": "\x00", "past the bound": strings.Repeat("x", 129)} {
		t.Run(name, func(t *testing.T) {
			failWith(t, session, "get_item_price", map[string]any{"catalogItemId": id}, "not-found")
			failWith(t, session, "get_price_quote", map[string]any{"quoteId": id}, "not-found")
		})
	}
	if len(reached) != 0 {
		t.Fatalf("identifiers %q reached a repository", reached)
	}

	// ...while an identifier exactly at the REST bound is a normal lookup.
	atBound := strings.Repeat("x", 128)
	failWith(t, session, "get_item_price", map[string]any{"catalogItemId": atBound}, "not-found")
	if len(reached) != 1 || reached[0] != atBound {
		t.Fatalf("an identifier of exactly 128 bytes must be looked up, repository saw %q", reached)
	}
}

// failingItemPrices is an ItemPriceRepository whose reads fail with a message
// that must never reach the caller.
type failingItemPrices struct{ ports.ItemPriceRepository }

var errBoom = errors.New("pq: connection to postgres://pricing:hunter2@db.internal:5432 refused")

func (failingItemPrices) Get(context.Context, itemprice.ID) (*itemprice.ItemPrice, error) {
	return nil, errBoom
}

// An unclassified error is internal-error, like REST's 500, and never carries
// the internal message.
func TestInternalFailureDoesNotLeak(t *testing.T) {
	h := newHarness(t)
	broken := h.all
	broken.ItemPrices = failingItemPrices{h.all.ItemPrices}
	session := connectSession(t, readDeps(broken))

	res := call(t, session, "get_item_price", map[string]any{"catalogItemId": "sku-1"})
	if !res.IsError || !strings.HasPrefix(text(res), "internal-error: ") {
		t.Fatalf("isError=%v text=%q, want internal-error", res.IsError, text(res))
	}
	if strings.Contains(text(res), "hunter2") || strings.Contains(text(res), "postgres://") {
		t.Fatalf("the tool error leaks the internal message: %q", text(res))
	}
}

// TestToolsNeverWrite proves the server is read-only end to end: after every
// kind of call, success or rejection, the store holds exactly what it held
// before (no row, no version bump, no outbox event).
func TestToolsNeverWrite(t *testing.T) {
	h := newHarness(t)
	id := h.quoted()
	h.saleRunningNow("sku-1", 900)
	before, eventsBefore := h.store.Counts(), len(h.store.Events())

	h.ok("get_item_price", map[string]any{"catalogItemId": "sku-1"})
	call(t, h.session, "get_item_price", map[string]any{"catalogItemId": "ghost"})
	h.ok("get_price_quote", map[string]any{"quoteId": id})
	h.clock.Advance(time.Hour) // the quote is now EXPIRED: still not stored by a read
	h.ok("get_price_quote", map[string]any{"quoteId": id})
	call(t, h.session, "get_price_quote", map[string]any{"quoteId": "ghost"})
	if _, err := h.session.CallTool(context.Background(), &sdk.CallToolParams{Name: "issue_price_quote"}); err == nil {
		t.Fatal("a write tool name must not exist")
	}

	if after := h.store.Counts(); after != before {
		t.Fatalf("store changed by read tools: before %+v after %+v", before, after)
	}
	if got := len(h.store.Events()); got != eventsBefore {
		t.Fatalf("outbox events %d -> %d: a read tool must publish nothing", eventsBefore, got)
	}
}
