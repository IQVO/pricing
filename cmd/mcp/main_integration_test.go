//go:build integration

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/pricing/internal/adapters/outbound/clock"
	"github.com/IQVO/pricing/internal/adapters/outbound/ids"
	outboundkafka "github.com/IQVO/pricing/internal/adapters/outbound/kafka"
	"github.com/IQVO/pricing/internal/adapters/outbound/postgres"
	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
	"github.com/IQVO/pricing/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

func brl(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: "BRL"} }

// seedPricing writes an item price with a running sale and an issued quote
// through the REAL command use cases on real Postgres (the writes the api would
// do), and returns the quote id.
func seedPricing(t *testing.T, dsn string) string {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	d := usecases.Deps{
		ItemPrices: postgres.NewItemPriceRepository(pool), Schedules: postgres.NewScheduleRepository(pool),
		TaxTables: postgres.NewTaxTableRepository(pool), Quotes: postgres.NewQuoteRepository(pool),
		Catalog: postgres.NewCatalogView(pool), Inbox: postgres.NewInbox(pool),
		Outbox: postgres.NewOutboxRepo(pool, outboundkafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
		Clock: clock.System{}, IDs: ids.Generator{}, Currency: "BRL",
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(usecases.Consumer{Deps: d}.CatalogItemCreated(ctx, "evt-sku-1", "t", "sku-1"))
	_, err = usecases.CreateItemPrice{Deps: d}.Execute(ctx, usecases.CreateItemPriceInput{CatalogItemID: "sku-1", ListPrice: brl(1000)})
	must(err)
	now := time.Now()
	_, err = usecases.ScheduleSale{Deps: d}.Execute(ctx, usecases.ScheduleSaleInput{CatalogItemID: "sku-1", Price: brl(800), From: now.Add(-time.Hour), To: now.Add(time.Hour)})
	must(err)
	var tiers []schedule.TierCharge
	for _, tier := range shared.Tiers() {
		tiers = append(tiers, schedule.TierCharge{Tier: tier, Charge: brl(500)})
	}
	_, err = usecases.PublishChargeSchedule{Deps: d}.Execute(ctx, usecases.PublishChargeScheduleInput{Version: 1, Tiers: tiers})
	must(err)
	_, err = usecases.PublishTaxTable{Deps: d}.Execute(ctx, usecases.PublishTaxTableInput{Version: 1, Bands: []tax.TaxBand{{PostalPrefixFrom: "000", PostalPrefixTo: "999", RateBasisPoints: 1000}}})
	must(err)
	q, err := usecases.IssuePriceQuote{Deps: d}.Execute(ctx, usecases.IssuePriceQuoteInput{
		CartID: "cart-1", DestinationPostalPrefix: "010",
		Lines:  []usecases.QuoteLine{{CatalogItemID: "sku-1", Quantity: 2}},
		Groups: []usecases.QuoteGroup{{GroupNo: 1, OfferID: "off-1", Tier: shared.TierStandard, LineNos: []int{1}}},
	})
	must(err)
	return q.QuoteID
}

func outboxRows(t *testing.T, dsn string) int {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var total int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

// freeAddr returns a loopback address nothing listens on right now.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitHealthy polls GET /healthz until the binary answers 200.
func waitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/healthz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the MCP binary never answered /healthz")
}

// startBinary boots the REAL entry point (run: embedded migrations, pgx pool,
// Postgres repositories, router) on a free port and returns its context and
// base URL once /healthz answers; the cleanup stops it and awaits the exit.
func startBinary(t *testing.T, dsn string) (context.Context, string) {
	t.Helper()
	addr := freeAddr(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("MCP_ADDR", addr)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:1")
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("the MCP binary did not stop after its context was cancelled")
		}
	})
	base := "http://" + addr
	waitHealthy(t, base)
	return ctx, base
}

// requireReadOnlyL1Tools asserts tools/list is exactly the two read tools,
// each L1 and annotated read-only.
func requireReadOnlyL1Tools(t *testing.T, tools []*sdk.Tool) {
	t.Helper()
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.Meta["com.warehouse/authority"] != "L1" || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s is not an L1 read-only tool: meta=%v annotations=%+v", tool.Name, tool.Meta, tool.Annotations)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"get_item_price", "get_price_quote"}) {
		t.Fatalf("tools = %v, want exactly get_item_price and get_price_quote", names)
	}
}

// requireNotFound asserts a tool result is the not-found tool error.
func requireNotFound(t *testing.T, name string, res *sdk.CallToolResult) {
	t.Helper()
	if !res.IsError || len(res.Content) == 0 {
		t.Fatalf("%s: %+v, want a not-found tool error", name, res)
	}
	if text, _ := res.Content[0].(*sdk.TextContent); text == nil || !strings.HasPrefix(text.Text, "not-found: ") {
		t.Fatalf("%s: %+v, want a not-found tool error", name, res)
	}
}

// amountMinor reads obj[key].amountMinor from a structured tool result.
func amountMinor(obj map[string]any, key string) any {
	money, _ := obj[key].(map[string]any)
	return money["amountMinor"]
}

// TestMCPBinaryOnRealPostgres boots the REAL entry point against a real
// Postgres, then over Streamable HTTP: /healthz answers, tools/list returns
// exactly the two read tools, both tools read what the command use cases
// persisted, an unknown or NUL-carrying id is a not-found tool error (not the
// 500 a NUL would cause in Postgres), and the reads leave the outbox as it was.
func TestMCPBinaryOnRealPostgres(t *testing.T) {
	dsn := pgtest.URL(t)
	quoteID := seedPricing(t, dsn)
	rowsBefore := outboxRows(t, dsn)
	ctx, base := startBinary(t, dsn)

	session, err := sdk.NewClient(&sdk.Implementation{Name: "it", Version: "0"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	requireReadOnlyL1Tools(t, tools.Tools)

	call := func(name string, args map[string]any) *sdk.CallToolResult {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: transport error: %v", name, err)
		}
		return res
	}
	item := call("get_item_price", map[string]any{"catalogItemId": "sku-1"})
	got, _ := item.StructuredContent.(map[string]any)
	if item.IsError || got["state"] != "ACTIVE" || amountMinor(got, "listPrice") != float64(1000) || amountMinor(got, "effectivePrice") != float64(800) {
		t.Fatalf("get_item_price = %v (isError=%v)", item.StructuredContent, item.IsError)
	}
	quote := call("get_price_quote", map[string]any{"quoteId": quoteID})
	q, _ := quote.StructuredContent.(map[string]any)
	if quote.IsError || q["state"] != "ISSUED" || q["quoteId"] != quoteID || q["cartId"] != "cart-1" {
		t.Fatalf("get_price_quote = %v (isError=%v)", quote.StructuredContent, quote.IsError)
	}
	requireNotFound(t, "unknown item", call("get_item_price", map[string]any{"catalogItemId": "ghost"}))
	requireNotFound(t, "NUL item", call("get_item_price", map[string]any{"catalogItemId": "sku\x00-1"}))
	requireNotFound(t, "unknown quote", call("get_price_quote", map[string]any{"quoteId": "ghost"}))
	requireNotFound(t, "NUL quote", call("get_price_quote", map[string]any{"quoteId": "q\x00"}))

	if rowsAfter := outboxRows(t, dsn); rowsAfter != rowsBefore {
		t.Fatalf("outbox rows %d -> %d: the read-only MCP server must write nothing", rowsBefore, rowsAfter)
	}
}
