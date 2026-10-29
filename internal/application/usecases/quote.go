package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/views"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// IssuePriceQuote is the command IssuePriceQuote (POST /price-quotes), called
// by checkout when a session opens. The quote prices exactly the lines and
// groups the session froze, at the effective price of each item now.
type IssuePriceQuote struct{ Deps }

// QuoteLine is one frozen cart line of the request.
type QuoteLine struct {
	CatalogItemID string
	Quantity      int
}

// QuoteGroup is one frozen shipment group of the request.
type QuoteGroup struct {
	GroupNo int
	OfferID string
	Tier    shared.Tier
	LineNos []int
}

// IssuePriceQuoteInput is the body of POST /price-quotes.
type IssuePriceQuoteInput struct {
	CartID                  string
	Lines                   []QuoteLine
	Groups                  []QuoteGroup
	DestinationPostalPrefix string
}

// Execute issues the quote and publishes PriceQuoteIssued (analytics).
func (u IssuePriceQuote) Execute(ctx context.Context, in IssuePriceQuoteInput) (views.PriceQuote, error) {
	v, err := u.issue(ctx, in)
	u.recordOutcome(ctx, err)
	return v, err
}

func (u IssuePriceQuote) issue(ctx context.Context, in IssuePriceQuoteInput) (views.PriceQuote, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.PriceQuote, error) {
		now := u.now()
		lines, err := u.priceLines(ctx, in.Lines, now)
		if err != nil {
			return views.PriceQuote{}, err
		}
		sched, err := u.Schedules.Current(ctx)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return views.PriceQuote{}, err
		}
		table, err := u.TaxTables.Current(ctx)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return views.PriceQuote{}, err
		}
		groups := make([]quote.GroupInput, len(in.Groups))
		for i, g := range in.Groups {
			groups[i] = quote.GroupInput{GroupNo: g.GroupNo, OfferID: g.OfferID, Tier: g.Tier, LineNos: g.LineNos}
		}
		q, events, err := quote.Issue(quote.IssueInput{
			ID: u.IDs.NewQuoteID(), CartID: in.CartID, DestinationPostalPrefix: in.DestinationPostalPrefix,
			Lines: lines, Groups: groups, Currency: u.Currency, TTL: u.quoteTTL(), Now: now,
		}, sched, table)
		if err != nil {
			return views.PriceQuote{}, err
		}
		if err := u.Quotes.Save(ctx, q); err != nil {
			return views.PriceQuote{}, err
		}
		if err := u.publish(ctx, events); err != nil {
			return views.PriceQuote{}, err
		}
		return views.PriceQuoteFrom(q, now), nil
	})
}

// priceLines looks up the price of every line: a line whose item has no price,
// or a withdrawn one, is handed to the domain unpriced (ErrItemNotPriced).
func (d Deps) priceLines(ctx context.Context, lines []QuoteLine, now time.Time) ([]quote.LineInput, error) {
	out := make([]quote.LineInput, len(lines))
	for i, l := range lines {
		out[i] = quote.LineInput{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
		if !shared.ValidID(l.CatalogItemID) {
			continue // the domain refuses the malformed id
		}
		p, err := d.ItemPrices.Get(ctx, itemprice.ID(l.CatalogItemID))
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if p.State() == itemprice.StateActive {
			out[i].Priced, out[i].Unit, out[i].PriceVersion = true, p.EffectivePrice(now), p.Version()
		}
	}
	return out, nil
}

// ExpireQuotes is the clock-driven policy QuoteTtl: it moves the ISSUED quotes
// past their expiresAt to EXPIRED. Nothing is published (model-gap decision 3).
type ExpireQuotes struct{ Deps }

// Execute expires one batch and returns how many quotes it expired. A quote
// another writer changed in between is skipped; the next pass sees it again.
func (u ExpireQuotes) Execute(ctx context.Context) (int, error) {
	ids, err := u.Quotes.DueForExpiry(ctx, u.now(), batchSize)
	if err != nil {
		return 0, err
	}
	expired := 0
	for _, id := range ids {
		_, err := transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (struct{}, error) {
			q, err := u.Quotes.Get(ctx, id)
			if err != nil {
				return struct{}{}, err
			}
			if err := q.Expire(u.now()); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, u.Quotes.Save(ctx, q)
		})
		switch {
		case err == nil:
			expired++
		case errors.Is(err, ports.ErrNotFound), errors.Is(err, ports.ErrConcurrentUpdate), errors.Is(err, shared.ErrIllegalTransition):
			u.logger().WarnContext(ctx, "quote expiry skipped", "quote_id", string(id), "error", err)
		default:
			return expired, err
		}
	}
	return expired, nil
}
