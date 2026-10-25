package kafka

import (
	"fmt"

	"github.com/IQVO/pricing/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// payloadFor maps a domain event to its wire payload and the stream (topic)
// it is published on.
func payloadFor(ev shared.Event) (data any, stream string, err error) {
	switch e := ev.(type) {
	case itemprice.ItemPriceChanged:
		return itemPriceChangedData{
			CatalogItemID: string(e.CatalogItemID), Currency: e.Currency, ListPriceMinor: e.ListPriceMinor,
			EffectivePriceMinor: e.EffectivePriceMinor, SaleEndsAt: timePtr(e.SaleEndsAt), Version: e.Version,
		}, cloudevents.StreamEvents, nil
	case itemprice.ItemPriceWithdrawn:
		return itemPriceWithdrawnData{CatalogItemID: string(e.CatalogItemID), Version: e.Version}, cloudevents.StreamEvents, nil
	case schedule.DeliveryChargeScheduleChanged:
		return schedulePayload(e), cloudevents.StreamEvents, nil
	case tax.TaxTableChanged:
		return taxPayload(e), cloudevents.StreamEvents, nil
	case quote.PriceQuoteIssued:
		return quoteIssuedData{
			QuoteID: string(e.QuoteID), CartID: e.CartID, Currency: e.Currency, TotalMinor: e.TotalMinor, ExpiresAt: e.ExpiresAt.UTC(),
		}, cloudevents.StreamAnalytics, nil
	}
	return nil, "", fmt.Errorf("kafka encoder: %T is not a published event", ev)
}

func schedulePayload(e schedule.DeliveryChargeScheduleChanged) scheduleChangedData {
	d := scheduleChangedData{ScheduleID: string(e.ScheduleID), Version: e.Version, Tiers: make([]tierData, len(e.Tiers))}
	for i, t := range e.Tiers {
		d.Tiers[i] = tierData{Tier: string(t.Tier), ChargeMinor: t.Charge.AmountMinor, FreeOverMinor: minorPtr(t.FreeOver)}
	}
	return d
}

func taxPayload(e tax.TaxTableChanged) taxChangedData {
	d := taxChangedData{TaxTableID: string(e.TaxTableID), Version: e.Version, Bands: make([]bandData, len(e.Bands))}
	for i, b := range e.Bands {
		d.Bands[i] = bandData(b)
	}
	return d
}
