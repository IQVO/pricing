package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// rejectionReasons maps a quote failure to the problem slug the REST adapter
// answers with (same vocabulary as apis/openapi.yaml), in match order.
var rejectionReasons = []struct {
	err    error
	reason string
}{
	{quote.ErrItemNotPriced, "item-not-priced"},
	{quote.ErrEmptyQuote, "empty-quote"},
	{quote.ErrNoChargeSchedule, "no-charge-schedule"},
	{quote.ErrNoTaxTable, "no-tax-table"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
	{shared.ErrNegativeAmount, "negative-amount"},
	{shared.ErrCurrencyMismatch, "currency-mismatch"},
	{shared.ErrValidation, "validation-failed"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// recordOutcome counts one quote attempt: issued when err is nil, else rejected
// with the reason of err.
func (u IssuePriceQuote) recordOutcome(ctx context.Context, err error) {
	if err == nil {
		u.metrics().QuoteIssued(ctx)
		return
	}
	u.metrics().QuoteRejected(ctx, rejectionReason(err))
}

func rejectionReason(err error) string {
	for _, r := range rejectionReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}
