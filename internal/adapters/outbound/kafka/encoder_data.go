package kafka

import (
	"time"

	"github.com/IQVO/pricing/internal/domain/shared"
)

// Topics of this service. PriceQuoteIssued is analytics-only and goes to
// TopicAnalytics; every other event goes to TopicEvents.
const (
	TopicEvents    = "retailnetwork.pricing.events"
	TopicAnalytics = "retailnetwork.pricing.analytics"
)

// schemaVersion is the dataschema version of every payload below.
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, camelCase JSON) pinned by
// apis/asyncapi.yaml. Money is flat `...Minor` integers, arrays are always
// present and optional fields are omitted when absent, never null.

type itemPriceChangedData struct {
	CatalogItemID       string     `json:"catalogItemId"`
	Currency            string     `json:"currency"`
	ListPriceMinor      int64      `json:"listPriceMinor"`
	EffectivePriceMinor int64      `json:"effectivePriceMinor"`
	SaleEndsAt          *time.Time `json:"saleEndsAt,omitempty"`
	Version             int64      `json:"version"`
}

type itemPriceWithdrawnData struct {
	CatalogItemID string `json:"catalogItemId"`
	Version       int64  `json:"version"`
}

type tierData struct {
	Tier          string `json:"tier"`
	ChargeMinor   int64  `json:"chargeMinor"`
	FreeOverMinor *int64 `json:"freeOverMinor,omitempty"`
}

type scheduleChangedData struct {
	ScheduleID string     `json:"scheduleId"`
	Version    int64      `json:"version"`
	Tiers      []tierData `json:"tiers"`
}

type bandData struct {
	PostalPrefixFrom string `json:"postalPrefixFrom"`
	PostalPrefixTo   string `json:"postalPrefixTo"`
	RateBasisPoints  int    `json:"rateBasisPoints"`
}

type taxChangedData struct {
	TaxTableID string     `json:"taxTableId"`
	Version    int64      `json:"version"`
	Bands      []bandData `json:"bands"`
}

type quoteIssuedData struct {
	QuoteID    string    `json:"quoteId"`
	CartID     string    `json:"cartId"`
	Currency   string    `json:"currency"`
	TotalMinor int64     `json:"totalMinor"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// minorPtr returns the amount of an optional Money, nil when absent.
func minorPtr(m shared.Money) *int64 {
	if m.IsZero() {
		return nil
	}
	v := m.AmountMinor
	return &v
}

// timePtr returns an optional instant in UTC, nil when zero.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}
