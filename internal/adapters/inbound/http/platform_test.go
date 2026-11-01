package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpadapter "github.com/IQVO/pricing/internal/adapters/inbound/http"
	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
)

const createBody = `{"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}`

// A 5xx is never stored: the retry with the same key runs the handler again,
// and only then is the answer stored and replayed.
func TestServerErrorIsNotStored(t *testing.T) {
	clock := memory.NewClock(t0)
	var calls int
	handler := httpadapter.RequireIdempotencyKey(memory.NewStore(clock).Idempotency(), clock)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		}))
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/item-prices", strings.NewReader(createBody))
		req.Header.Set("Idempotency-Key", "k-5xx")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := send(); rec.Code != http.StatusInternalServerError || rec.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("first = %d %v", rec.Code, rec.Header())
	}
	if rec := send(); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "" || calls != 2 {
		t.Fatalf("retry = %d %v after %d calls, want a fresh 201 from the second call", rec.Code, rec.Header(), calls)
	}
	if rec := send(); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "true" || calls != 2 {
		t.Fatalf("replay = %d %v after %d calls, want the stored 201 without a third call", rec.Code, rec.Header(), calls)
	}
}

func TestIdempotencyKeyRequiredAndReplay(t *testing.T) {
	e := newEnv(t)
	e.catalogued("sku-1")
	e.wantProblem(e.do(http.MethodPost, "/item-prices", "", createBody), http.StatusBadRequest, "idempotency-key-required")
	// A key that is present but not 1-128 printable ASCII characters is idempotency-key-invalid.
	e.wantProblem(e.do(http.MethodPost, "/item-prices", strings.Repeat("k", 129), createBody), http.StatusBadRequest, "idempotency-key-invalid")
	e.wantProblem(e.do(http.MethodPost, "/item-prices", "caf\u00e9", createBody), http.StatusBadRequest, "idempotency-key-invalid")
	// 128 printable ASCII characters is the longest valid key.
	e.catalogued("sku-2")
	longest := `{"catalogItemId":"sku-2","listPrice":{"amountMinor":1000,"currency":"BRL"}}`
	e.wantStatus(e.do(http.MethodPost, "/item-prices", strings.Repeat("k", 128), longest), http.StatusCreated)

	first := e.do(http.MethodPost, "/item-prices", "k1", createBody)
	e.wantStatus(first, http.StatusCreated)
	if got := first.Header.Get("Idempotency-Replayed"); got != "" {
		t.Fatalf("the first execution must not carry Idempotency-Replayed, got %q", got)
	}
	again := e.do(http.MethodPost, "/item-prices", "k1", createBody)
	e.wantStatus(again, http.StatusCreated)
	if again.Body != first.Body || again.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay differs: %q vs %q", again.Body, first.Body)
	}
	if again.Header.Get("Content-Type") != first.Header.Get("Content-Type") {
		t.Fatalf("replay content-type = %q, want %q", again.Header.Get("Content-Type"), first.Header.Get("Content-Type"))
	}
}

// The key is global: the same key with another body, or on another route, is a
// reuse (409), never a new key (REST guideline 2.3).
func TestIdempotencyKeyReusedIsConflict(t *testing.T) {
	e := newEnv(t)
	e.catalogued("sku-1")
	e.wantStatus(e.do(http.MethodPost, "/item-prices", "k1", createBody), http.StatusCreated)
	other := `{"catalogItemId":"sku-1","listPrice":{"amountMinor":5,"currency":"BRL"}}`
	e.wantProblem(e.do(http.MethodPost, "/item-prices", "k1", other), http.StatusConflict, "idempotency-key-reused")
	e.wantProblem(e.do(http.MethodPost, "/item-prices/sku-1/withdrawals", "k1", ""), http.StatusConflict, "idempotency-key-reused")
	// The refused reuse left the first answer intact.
	again := e.do(http.MethodPost, "/item-prices", "k1", createBody)
	e.wantStatus(again, http.StatusCreated)
	if again.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("the original request must still replay: %v", again.Header)
	}
}

// A PUT takes no middleware: a key is accepted and ignored, a missing one is fine.
func TestPutNeedsNoIdempotencyKey(t *testing.T) {
	e := newEnv(t)
	e.priced("sku-1", 1000)
	body := `{"listPrice":` + money(1500) + `}`
	e.wantStatus(e.do(http.MethodPut, "/item-prices/sku-1/list-price", "", body), http.StatusOK)
	r := e.do(http.MethodPut, "/item-prices/sku-1/list-price", "ignored", body)
	e.wantStatus(r, http.StatusOK)
	if r.Header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("a PUT never replays: %v", r.Header)
	}
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
