//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/IQVO/pricing/internal/adapters/inbound/a2a"
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

func outboxRows(t *testing.T, dsn string) (total, unpublished int) {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	err = pool.QueryRow(context.Background(), `SELECT count(*), count(*) FILTER (WHERE published_at IS NULL) FROM outbox_events`).Scan(&total, &unpublished)
	if err != nil {
		t.Fatal(err)
	}
	return total, unpublished
}

// TestA2AReadSkillsOnRealPostgres boots the real composition (embedded
// migrations, pgx pool, Postgres repositories) and drives both skills over the
// JSON-RPC wire against real rows: the answers match what was written, an
// unknown or NUL-carrying identifier is a 404 problem (not the 500 a NUL would
// cause in Postgres), and the reads leave the outbox exactly as it was.
func TestA2AReadSkillsOnRealPostgres(t *testing.T) {
	dsn := pgtest.URL(t)
	quoteID := seedPricing(t, dsn)
	rowsBefore, _ := outboxRows(t, dsn)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps, closeDeps, err := buildDeps(context.Background(), bootConfig{databaseURL: dsn, migrationsURL: dsn, agentURL: testAgentURL}, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeDeps)

	srv := httptest.NewServer(newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), inbounda2a.Card(inbounda2a.Config{AgentURL: testAgentURL}), "pricing-a2a-it"))
	t.Cleanup(srv.Close)
	client, err := a2aclient.NewFromCard(context.Background(), inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/a2a", Version: "it"}))
	if err != nil {
		t.Fatal(err)
	}
	ask := func(skill string, args map[string]any) (data map[string]any, problem map[string]any) {
		t.Helper()
		resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))})
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		msg := resp.(*a2a.Message)
		raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
		var env struct {
			Data    map[string]any `json:"data"`
			Problem map[string]any `json:"problem"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		return env.Data, env.Problem
	}

	data, problem := ask("item_price", map[string]any{"catalogItemId": "sku-1"})
	item, _ := data["itemPrice"].(map[string]any)
	if problem != nil || item == nil || item["state"] != "ACTIVE" ||
		item["listPrice"].(map[string]any)["amountMinor"] != float64(1000) || item["effectivePrice"].(map[string]any)["amountMinor"] != float64(800) {
		t.Fatalf("item_price = %v / %v", data, problem)
	}

	data, problem = ask("quote_status", map[string]any{"quoteId": quoteID})
	q, _ := data["priceQuote"].(map[string]any)
	if problem != nil || q == nil || q["state"] != "ISSUED" || q["cartId"] != "cart-1" || q["quoteId"] != quoteID {
		t.Fatalf("quote_status = %v / %v", data, problem)
	}

	for name, call := range map[string]func() map[string]any{
		"unknown item":  func() map[string]any { _, p := ask("item_price", map[string]any{"catalogItemId": "ghost"}); return p },
		"unknown quote": func() map[string]any { _, p := ask("quote_status", map[string]any{"quoteId": "ghost"}); return p },
		"NUL item": func() map[string]any {
			_, p := ask("item_price", map[string]any{"catalogItemId": "sku\x00-1"})
			return p
		},
		"NUL quote": func() map[string]any { _, p := ask("quote_status", map[string]any{"quoteId": "q\x00"}); return p },
	} {
		p := call()
		if p == nil || p["status"] != float64(404) || !strings.HasSuffix(p["type"].(string), "/not-found") {
			t.Fatalf("%s: problem = %v, want a 404 not-found answer", name, p)
		}
	}

	if rowsAfter, _ := outboxRows(t, dsn); rowsAfter != rowsBefore {
		t.Fatalf("outbox rows %d -> %d: the read-only agent must write nothing", rowsBefore, rowsAfter)
	}
}
