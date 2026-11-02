package http

import (
	"errors"
	"net/http"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// problemBase is the namespace of this service's RFC 9457 `type` URIs.
const problemBase = "/problems/"

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type).
var errMalformedRequest = errors.New("request is not valid for this operation")

// problem is the fixed (status, slug, title) of one error category; the detail
// comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the adapter writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is malformed"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
	invalidLimit           = problem{http.StatusBadRequest, "invalid-limit", "Invalid limit"}
	invalidCursor          = problem{http.StatusBadRequest, "invalid-cursor", "Invalid cursor"}
	unknownQueryParameter  = problem{http.StatusBadRequest, "unknown-query-parameter", "Unknown query parameter"}
)

func unprocessable(slug, title string) problem {
	return problem{http.StatusUnprocessableEntity, slug, title}
}

func conflict(slug, title string) problem {
	return problem{http.StatusConflict, slug, title}
}

var notFound = problem{http.StatusNotFound, "not-found", "Not found"}

// problemCatalogue maps every typed error to its problem, in match order. The
// slugs are the ones of apis/openapi.yaml and .claude/rules/rest-api.md.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{ports.ErrNotFound, notFound},
	{itemprice.ErrOverlappingSaleWindow, conflict("overlapping-sale-window", "Sale windows of one item never overlap")},
	{schedule.ErrNonIncreasingVersion, conflict("non-increasing-version", "A published version must exceed the current one")},
	{tax.ErrNonIncreasingVersion, conflict("non-increasing-version", "A published version must exceed the current one")},
	{ports.ErrConcurrentUpdate, conflict("concurrent-update", "The resource was modified by another request; retry")},
	{shared.ErrIllegalTransition, conflict("illegal-transition", "The resource does not allow this change in its current state")},
	{itemprice.ErrUnknownSale, notFound},
	{itemprice.ErrUnknownCatalogItem, unprocessable("unknown-catalog-item", "The catalogue item is not known to this service")},
	{itemprice.ErrSaleNotBelowList, unprocessable("sale-not-below-list", "A sale price is always below the list price")},
	{schedule.ErrIncompleteChargeSchedule, unprocessable("incomplete-charge-schedule", "A schedule names a charge for every service tier")},
	{tax.ErrOverlappingTaxBand, unprocessable("overlapping-tax-band", "Tax bands never overlap")},
	{tax.ErrRateOutOfRange, unprocessable("rate-out-of-range", "A tax rate is between 0 and 10000 basis points")},
	{quote.ErrItemNotPriced, unprocessable("item-not-priced", "A quoted item has no active price")},
	{quote.ErrEmptyQuote, unprocessable("empty-quote", "A quote has at least one line")},
	{quote.ErrNoChargeSchedule, unprocessable("no-charge-schedule", "No delivery charge schedule is current")},
	{quote.ErrNoTaxTable, unprocessable("no-tax-table", "No tax table is current")},
	{shared.ErrNegativeAmount, unprocessable("negative-amount", "An amount is never negative")},
	{shared.ErrCurrencyMismatch, unprocessable("currency-mismatch", "An amount is in the configured currency")},
	{shared.ErrValidation, unprocessable("validation-failed", "Validation failed")},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
