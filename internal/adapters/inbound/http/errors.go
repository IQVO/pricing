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

// replayClass is what the Idempotency-Key middleware does with the answer of a
// problem type (ADR 0011): FINAL and OUTCOME answers are stored and replayed, a
// RETRYABLE one is not stored and its key is released. A 2xx is stored and a 5xx
// never is, whatever the class. The empty class is "unclassified".
type replayClass string

const (
	// replayFinal: the answer is a function of the request (or of state that
	// only ever moves one way) alone; stored and replayed.
	replayFinal replayClass = "FINAL"
	// replayOutcome: the command committed state and then refused; the writes
	// commit with the stored answer. No problem type of this service is one
	// (no keyed use case writes before it refuses; ADR 0011).
	replayOutcome replayClass = "OUTCOME"
	// replayRetryable: nothing committed and the answer depends on state that
	// can change; not stored, the key is released, a retry executes again.
	replayRetryable replayClass = "RETRYABLE"
)

// stored reports whether an error answer of this class is remembered under its key.
func (c replayClass) stored() bool { return c == replayFinal || c == replayOutcome }

// catalogueEntry is one typed error, its problem and the replay class of that problem.
type catalogueEntry struct {
	err    error
	p      problem
	replay replayClass
}

// problemCatalogue maps every typed error to its problem and its replay class,
// in match order. The slugs are the ones of apis/openapi.yaml and
// .claude/rules/rest-api.md. ADR 0011 holds the reason of every class and a test
// keeps its table equal to this one; a new entry without a class fails
// TestEveryProblemTypeDeclaresAReplayClass. When unsure the class is RETRYABLE: a
// refusal that rolled everything back costs one more execution, a wrongly stored
// one pins a stale answer for 24 hours.
var problemCatalogue = []catalogueEntry{
	// The body is not valid JSON for the operation: a function of the request (decodeJSON, response.go).
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}, replayFinal},
	// Shared slug: the item price of the path (an id offer-availability mints, delivered by an event that can
	// arrive after the request) and a sale id of the path (ErrUnknownSale below): mixed causes, so RETRYABLE.
	{ports.ErrNotFound, notFound, replayRetryable},
	// The new window is judged against the live windows of the item (itemprice.go ScheduleSale): a cancelled
	// or ended window stops overlapping, so a retry can pass.
	{itemprice.ErrOverlappingSaleWindow, conflict("overlapping-sale-window", "Sale windows of one item never overlap"), replayRetryable},
	// Versions strictly increase (CLAUDE.md rule 6) and nothing is ever deleted, so the current version only
	// moves up: a version that does not exceed it today never will (schedule.go Publish, tax.go Publish).
	{schedule.ErrNonIncreasingVersion, conflict("non-increasing-version", "A published version must exceed the current one"), replayFinal},
	{tax.ErrNonIncreasingVersion, conflict("non-increasing-version", "A published version must exceed the current one"), replayFinal},
	// The optimistic-lock guard or a unique-index race (itemprices.go writeItemPrice) after the use case's own
	// commandAttempts: another writer got in between; its request may win on the retry.
	{ports.ErrConcurrentUpdate, conflict("concurrent-update", "The resource was modified by another request; retry"), replayRetryable},
	// Withdrawing an ACTIVE price, cancelling a live sale: the aggregate state other requests move.
	{shared.ErrIllegalTransition, conflict("illegal-transition", "The resource does not allow this change in its current state"), replayRetryable},
	// A sale id this service mints and never deletes, but the slug is shared with ports.ErrNotFound's mixed
	// causes above: one class per slug, so RETRYABLE until the slug is split (recorded follow-up, ADR 0011).
	{itemprice.ErrUnknownSale, notFound, replayRetryable},
	// The local CatalogItemView is fed by offer-availability's events (ADR 0003): an item unknown now can be
	// known a moment later, and a discontinued item can never be priced (itemprice.go checkCatalog).
	{itemprice.ErrUnknownCatalogItem, unprocessable("unknown-catalog-item", "The catalogue item is not known to this service"), replayRetryable},
	// Judged against the item's current list price (itemprice.go ScheduleSale), which PUT list-price moves.
	{itemprice.ErrSaleNotBelowList, unprocessable("sale-not-below-list", "A sale price is always below the list price"), replayRetryable},
	// The tier set is the fixed enum shared.Tiers() (schedule.go orderTiers): the request alone.
	{schedule.ErrIncompleteChargeSchedule, unprocessable("incomplete-charge-schedule", "A schedule names a charge for every service tier"), replayFinal},
	// The bands of the request against each other (tax.go): the request alone.
	{tax.ErrOverlappingTaxBand, unprocessable("overlapping-tax-band", "Tax bands never overlap"), replayFinal},
	// A band rate of the request (tax.go): the request alone.
	{tax.ErrRateOutOfRange, unprocessable("rate-out-of-range", "A tax rate is between 0 and 10000 basis points"), replayFinal},
	// A line whose item has no ACTIVE price now (quote.go priceLines): prices are created and withdrawn by
	// other requests, so a retry can pass.
	{quote.ErrItemNotPriced, unprocessable("item-not-priced", "A quoted item has no active price"), replayRetryable},
	// A quote has at least one line: the request alone (quote.go checkShape).
	{quote.ErrEmptyQuote, unprocessable("empty-quote", "A quote has at least one line"), replayFinal},
	// A CURRENT schedule is published by another request (quote.go Issue: sched == nil): a referenced fact
	// that is simply not there yet.
	{quote.ErrNoChargeSchedule, unprocessable("no-charge-schedule", "No delivery charge schedule is current"), replayRetryable},
	// A CURRENT tax table the same way (quote.go Issue: table == nil).
	{quote.ErrNoTaxTable, unprocessable("no-tax-table", "No tax table is current"), replayRetryable},
	// An amount of the request (shared/money.go Check): the request alone.
	{shared.ErrNegativeAmount, unprocessable("negative-amount", "An amount is never negative"), replayFinal},
	// The request's currency against the one configured currency (shared/money.go Check): a function of the
	// request (the configuration is fixed at deploy time).
	{shared.ErrCurrencyMismatch, unprocessable("currency-mismatch", "An amount is in the configured currency"), replayFinal},
	// Mixed: most causes are fields of the request, but "an item holds at most 50 sale windows"
	// (itemprice.go ScheduleSale) counts the item's live windows. One class per slug, so RETRYABLE until the
	// slug is split (recorded follow-up, ADR 0011).
	{shared.ErrValidation, unprocessable("validation-failed", "Validation failed"), replayRetryable},
}

// entryFor returns the first catalogue entry err matches; an unmatched error is
// the internal-error problem with no class.
func entryFor(err error) catalogueEntry {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry
		}
	}
	return catalogueEntry{p: internalProblem}
}
