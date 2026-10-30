package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	httpadapter "github.com/IQVO/pricing/internal/adapters/inbound/http"
	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
	"github.com/IQVO/pricing/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	store *memory.Store
	clock *memory.Clock
	deps  usecases.Deps
	ready *httpadapter.Readiness
	srv   *httptest.Server
	keys  int
}

func newEnv(t *testing.T) *env { return newEnvLogged(t, slog.New(slog.DiscardHandler)) }

// newEnvLogged is newEnv with the router's request logger set (newEnv discards
// it so the access lines do not flood the test output).
func newEnvLogged(t *testing.T, logger *slog.Logger) *env {
	t.Helper()
	clock := memory.NewClock(t0)
	s := memory.NewStore(clock)
	deps := usecases.Deps{
		ItemPrices: s.ItemPrices(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Quotes: s.Quotes(),
		Catalog: s.Catalog(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "BRL",
	}
	ready := httpadapter.NewReadiness(0, 0)
	api := &httpadapter.Server{
		CreateItemPrice: usecases.CreateItemPrice{Deps: deps}, SetListPrice: usecases.SetListPrice{Deps: deps},
		ScheduleSale: usecases.ScheduleSale{Deps: deps}, CancelSale: usecases.CancelSale{Deps: deps},
		WithdrawPrice: usecases.WithdrawPrice{Deps: deps}, GetItemPrice: usecases.GetItemPrice{Deps: deps},
		ListItemPrices: usecases.ListItemPrices{Deps: deps}, PublishChargeSchedule: usecases.PublishChargeSchedule{Deps: deps},
		GetChargeSchedule: usecases.GetCurrentChargeSchedule{Deps: deps}, PublishTaxTable: usecases.PublishTaxTable{Deps: deps},
		GetTaxTable: usecases.GetCurrentTaxTable{Deps: deps}, IssuePriceQuote: usecases.IssuePriceQuote{Deps: deps},
		GetPriceQuote: usecases.GetPriceQuote{Deps: deps}, IdempotencyStore: s.Idempotency(), Clock: clock, Readiness: ready,
	}
	srv := httptest.NewServer(httpadapter.NewRouter(api, logger, ""))
	t.Cleanup(srv.Close)
	return &env{t: t, store: s, clock: clock, deps: deps, ready: ready, srv: srv}
}

type reply struct {
	Status int
	Header http.Header
	Body   string
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatalf("body is not JSON: %q (%v)", r.Body, err)
	}
	return m
}

// do sends one request; key "" sends no Idempotency-Key.
func (e *env) do(method, path, key, body string) reply {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: string(b)}
}

// post sends a POST with a fresh Idempotency-Key.
func (e *env) post(path, body string) reply {
	e.t.Helper()
	e.keys++
	return e.do(http.MethodPost, path, "key-"+strconv.Itoa(e.keys), body)
}

func (e *env) get(path string) reply { e.t.Helper(); return e.do(http.MethodGet, path, "", "") }

func (e *env) wantStatus(r reply, status int) {
	e.t.Helper()
	if r.Status != status {
		e.t.Fatalf("status = %d, want %d: %s", r.Status, status, r.Body)
	}
}

func (e *env) wantProblem(r reply, status int, slug string) {
	e.t.Helper()
	e.wantStatus(r, status)
	if ct := r.Header.Get("Content-Type"); ct != "application/problem+json" {
		e.t.Fatalf("content-type = %q", ct)
	}
	if got := r.json(e.t)["type"]; got != "/problems/"+slug {
		e.t.Fatalf("problem type = %v, want /problems/%s (%s)", got, slug, r.Body)
	}
}

// catalogued feeds CatalogItemCreated for the item.
func (e *env) catalogued(id string) {
	e.t.Helper()
	err := (usecases.Consumer{Deps: e.deps}).CatalogItemCreated(context.Background(), "evt-"+id, "t", id)
	if err != nil {
		e.t.Fatal(err)
	}
}

func money(n int64) string { return fmt.Sprintf(`{"amountMinor":%d,"currency":"BRL"}`, n) }

// priced creates a catalogued item with the list price.
func (e *env) priced(id string, list int64) {
	e.t.Helper()
	e.catalogued(id)
	body := fmt.Sprintf(`{"catalogItemId":%q,"listPrice":%s}`, id, money(list))
	e.wantStatus(e.post("/item-prices", body), http.StatusCreated)
}
