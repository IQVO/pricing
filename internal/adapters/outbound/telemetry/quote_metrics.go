package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/IQVO/pricing/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/IQVO/pricing"

// quotesIssuedName is the Tier 2 business metric of the context (the fleet's
// standard metrics convention): every price-quote request, by outcome.
const quotesIssuedName = "pricing.quotes.issued"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeIssued   = "issued"
	outcomeRejected = "rejected"
)

// QuoteMetrics implements ports.QuoteMetrics against the global MeterProvider.
// Until Setup installs a real provider the global one is a no-op, so recording
// is cheap and safe in tests and local runs.
type QuoteMetrics struct {
	counter metric.Int64Counter
}

var _ ports.QuoteMetrics = (*QuoteMetrics)(nil)

// NewQuoteMetrics registers the counter. It only fails if the instrument name
// is invalid, which is a programming error, not a runtime condition. Call it
// after Setup so the instrument binds to the real MeterProvider.
func NewQuoteMetrics() (*QuoteMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		quotesIssuedName,
		metric.WithDescription("Price-quote requests by outcome (issued or rejected; reason says why a rejection happened, "+
			"as a problem slug such as item-not-priced or no-tax-table). "+
			"A falling issued share with a rising item-not-priced means shoppers are carting items that have no active price "+
			"(a catalogue/pricing gap, not shopper input); a rising no-charge-schedule or no-tax-table means a schedule or "+
			"table was never published, so every checkout fails until one is; a rising internal-error means the service itself is failing."),
		metric.WithUnit("{quote}"),
	)
	if err != nil {
		return nil, err
	}
	return &QuoteMetrics{counter: counter}, nil
}

// QuoteIssued counts a quote that was issued.
func (m *QuoteMetrics) QuoteIssued(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeIssued)))
}

// QuoteRejected counts a request that issued no quote, with the reason why.
func (m *QuoteMetrics) QuoteRejected(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRejected), reasonKey.String(reason)))
}
