// Package http is the REST inbound adapter: every route of apis/openapi.yaml on
// a chi router, RFC 9457 problem documents for every error and the liveness and
// readiness endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/IQVO/pricing/internal/adapters/inbound/requestlog"
	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/usecases"
)

// Route names under which the Idempotency-Key records are stored.
const (
	routeCreateItemPrice = "POST /item-prices"
	routeSetListPrice    = "POST /item-prices/{catalogItemId}:set-list-price"
	routeWithdraw        = "POST /item-prices/{catalogItemId}:withdraw"
	routeScheduleSale    = "POST /item-prices/{catalogItemId}/sales"
	routeCancelSale      = "POST /item-prices/{catalogItemId}/sales/{saleId}:cancel"
	routePublishSchedule = "POST /delivery-charge-schedules"
	routePublishTax      = "POST /tax-tables"
	routeIssueQuote      = "POST /price-quotes"
)

// Server holds the use cases the handlers call.
type Server struct {
	CreateItemPrice usecases.CreateItemPrice
	SetListPrice    usecases.SetListPrice
	ScheduleSale    usecases.ScheduleSale
	CancelSale      usecases.CancelSale
	WithdrawPrice   usecases.WithdrawPrice
	GetItemPrice    usecases.GetItemPrice
	ListItemPrices  usecases.ListItemPrices

	PublishChargeSchedule usecases.PublishChargeSchedule
	GetChargeSchedule     usecases.GetCurrentChargeSchedule
	PublishTaxTable       usecases.PublishTaxTable
	GetTaxTable           usecases.GetCurrentTaxTable

	IssuePriceQuote usecases.IssuePriceQuote
	GetPriceQuote   usecases.GetPriceQuote

	// IdempotencyStore and Clock back the Idempotency-Key middleware of the
	// POST routes.
	IdempotencyStore ports.IdempotencyStore
	Clock            ports.Clock
	// Readiness backs GET /readyz; nil means always ready (unit tests).
	Readiness *Readiness
}

// DefaultServiceName names the server spans and the HTTP metrics when the
// composition root passes no service name.
const DefaultServiceName = "pricing"

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml. A nil
// logger defaults to slog.Default(); an empty serviceName to DefaultServiceName.
//
// Middleware order is the fleet observability standard: RequestID, then otelchi
// (the server span, named after the chi route pattern, so one span name per
// route and not per catalogue item id), then the request-duration histogram,
// then RequestLogger (it runs inside the span, so its record carries trace_id
// and span_id), then Recoverer. telemetry.Setup must have run before this call:
// otelchimetric binds the global MeterProvider when it is constructed.
func NewRouter(s *Server, logger *slog.Logger, serviceName string) http.Handler {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	// A NUL (U+0000) in the path, the query or a body is a uniform client error,
	// never a Postgres 22021 -> 500; it runs before the Idempotency-Key
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.With(s.idempotent(routeCreateItemPrice)).Post("/item-prices", s.handleCreateItemPrice)
	r.Get("/item-prices", s.handleListItemPrices)
	r.Get("/item-prices/{catalogItemId}", s.handleGetItemPrice)
	r.With(s.idempotent(routeSetListPrice)).Post("/item-prices/{catalogItemId}:set-list-price", s.handleSetListPrice)
	r.With(s.idempotent(routeWithdraw)).Post("/item-prices/{catalogItemId}:withdraw", s.handleWithdraw)
	r.With(s.idempotent(routeScheduleSale)).Post("/item-prices/{catalogItemId}/sales", s.handleScheduleSale)
	r.With(s.idempotent(routeCancelSale)).Post("/item-prices/{catalogItemId}/sales/{saleId}:cancel", s.handleCancelSale)

	r.With(s.idempotent(routePublishSchedule)).Post("/delivery-charge-schedules", s.handlePublishSchedule)
	r.Get("/delivery-charge-schedules/current", s.handleGetSchedule)
	r.With(s.idempotent(routePublishTax)).Post("/tax-tables", s.handlePublishTax)
	r.Get("/tax-tables/current", s.handleGetTax)

	r.With(s.idempotent(routeIssueQuote)).Post("/price-quotes", s.handleIssueQuote)
	r.Get("/price-quotes/{quoteId}", s.handleGetQuote)
	return r
}

func (s *Server) idempotent(route string) func(http.Handler) http.Handler {
	return RequireIdempotencyKey(s.IdempotencyStore, s.Clock, route)
}
