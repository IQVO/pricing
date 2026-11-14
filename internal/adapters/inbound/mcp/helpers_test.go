package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/pricing/internal/adapters/inbound/mcp"
	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// now is the fixture's fixed "now".
var now = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

func brl(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: "BRL"} }

// harness is a connected in-memory MCP client over the real server, wired to
// the query use cases over in-memory repositories exactly as cmd/mcp wires
// them (minus Postgres). The command use cases exist ONLY here, to seed data
// the way the REST adapter would: the MCP surface itself has none.
type harness struct {
	t       *testing.T
	session *sdk.ClientSession
	store   *memory.Store
	clock   *memory.Clock
	all     usecases.Deps
}

func readDeps(all usecases.Deps) inboundmcp.Deps {
	return inboundmcp.Deps{GetItemPrice: usecases.GetItemPrice{Deps: all}, GetPriceQuote: usecases.GetPriceQuote{Deps: all}}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clock := memory.NewClock(now)
	s := memory.NewStore(clock)
	all := usecases.Deps{
		ItemPrices: s.ItemPrices(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Quotes: s.Quotes(),
		Catalog: s.Catalog(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "BRL",
	}
	return &harness{t: t, session: connectSession(t, readDeps(all)), store: s, clock: clock, all: all}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
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
		CatalogItemID: id, Price: brl(price), From: now.Add(-time.Hour), To: now.Add(24 * time.Hour),
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

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(name string, args map[string]any) map[string]any {
	h.t.Helper()
	res := call(h.t, h.session, name, args)
	if res.IsError {
		h.t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		h.t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts
// with the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
