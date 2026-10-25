package http_test

import (
	"net/http"
	"strings"
	"testing"
)

const createBody = `{"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}`

func TestIdempotencyKeyRequiredAndReplay(t *testing.T) {
	e := newEnv(t)
	e.catalogued("sku-1")
	e.wantProblem(e.do(http.MethodPost, "/item-prices", "", createBody), http.StatusBadRequest, "idempotency-key-required")
	e.wantProblem(e.do(http.MethodPost, "/item-prices", strings.Repeat("k", 129), createBody), http.StatusBadRequest, "idempotency-key-required")

	first := e.do(http.MethodPost, "/item-prices", "k1", createBody)
	e.wantStatus(first, http.StatusCreated)
	again := e.do(http.MethodPost, "/item-prices", "k1", createBody)
	e.wantStatus(again, http.StatusCreated)
	if again.Body != first.Body || again.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay differs: %q vs %q", again.Body, first.Body)
	}
	other := `{"catalogItemId":"sku-1","listPrice":{"amountMinor":5,"currency":"BRL"}}`
	e.wantProblem(e.do(http.MethodPost, "/item-prices", "k1", other), http.StatusUnprocessableEntity, "idempotency-key-reused")
}

func TestIdempotencyRecordsClientErrorsNotServerErrors(t *testing.T) {
	e := newEnv(t)
	bad := e.do(http.MethodPost, "/item-prices", "k2", createBody)
	e.wantProblem(bad, http.StatusUnprocessableEntity, "unknown-catalog-item")
	e.catalogued("sku-1")
	// The client error was stored: the key replays it even though the item exists now.
	again := e.do(http.MethodPost, "/item-prices", "k2", createBody)
	e.wantProblem(again, http.StatusUnprocessableEntity, "unknown-catalog-item")
}

func TestOversizedBodyIsMalformed(t *testing.T) {
	e := newEnv(t)
	big := `{"catalogItemId":"` + strings.Repeat("a", 70<<10) + `"}`
	e.wantProblem(e.post("/item-prices", big), http.StatusBadRequest, "malformed-request")
}

func TestMalformedBodies(t *testing.T) {
	e := newEnv(t)
	for _, body := range []string{``, `{`, `{"unknown":1}`, createBody + `{}`} {
		e.wantProblem(e.post("/item-prices", body), http.StatusBadRequest, "malformed-request")
	}
}

func TestRejectNUL(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.get("/item-prices/a%00b"), http.StatusNotFound, "resource-not-found")
	e.wantProblem(e.get("/item-prices?after=a%00b"), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/item-prices", `{"catalogItemId":"a\u0000b"}`), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/item-prices", "{\"catalogItemId\":\"a\x00b\"}"), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/item-prices", `{"catalogItemId":"a\\u0000b"}`), http.StatusUnprocessableEntity, "validation-failed")
}

func TestHealthAndReadiness(t *testing.T) {
	e := newEnv(t)
	e.wantStatus(e.get("/healthz"), http.StatusOK)
	e.wantStatus(e.get("/readyz"), http.StatusOK)
	e.ready.SetNotReady()
	e.wantStatus(e.get("/readyz"), http.StatusServiceUnavailable)
	e.wantStatus(e.get("/healthz"), http.StatusOK)
}

func TestOverlongPathIDIsNotFound(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.get("/item-prices/"+strings.Repeat("a", 129)), http.StatusNotFound, "not-found")
	e.wantProblem(e.get("/price-quotes/"+strings.Repeat("a", 129)), http.StatusNotFound, "not-found")
}
