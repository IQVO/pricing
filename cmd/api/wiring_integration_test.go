//go:build integration

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/testing/kafkatest"
	"github.com/IQVO/pricing/internal/testing/pgtest"
)

const (
	typeCreated      = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated"
	typeDiscontinued = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued"
)

// bootReal boots the real entry point under a consumer group of its own, so
// a test never inherits the committed offsets of another.
func bootReal(t *testing.T, group string) *service {
	kafkatest.CreateTopic(t, topicEvents)
	kafkatest.CreateTopic(t, topicAnalytics)
	kafkatest.CreateTopic(t, topicAvailability)
	return boot(t, map[string]string{
		"DATABASE_URL": pgtest.URL(t), "KAFKA_BROKERS": strings.Join(kafkatest.Brokers(t), ","),
		"OFFER_AVAILABILITY_CONSUMER_GROUP": "pricing.e2e." + group, "OUTBOX_RELAY_INTERVAL": "100ms",
		"POLICY_INTERVAL": "200ms", "SHUTDOWN_DRAIN_DELAY": "0",
	})
}

func money(n int) string { return fmt.Sprintf(`{"amountMinor":%d,"currency":"BRL"}`, n) }

// priceOnceKnown creates the item price as soon as the consumer has learned the item.
func priceOnceKnown(t *testing.T, s *service, item, key string) {
	t.Helper()
	body := fmt.Sprintf(`{"catalogItemId":%q,"listPrice":%s}`, item, money(1000))
	// A client error is stored under its key, so every attempt uses a fresh one.
	attempt := 0
	eventually(t, 60*time.Second, "the consumed item to be known", func() bool {
		attempt++
		return s.call(t, "POST", "/item-prices", fmt.Sprintf("%s-%d", key, attempt), body).status == 201
	})
}

func TestRealWiringPublishesTheEffectivePriceOfAConsumedItem(t *testing.T) {
	s := bootReal(t, "publish")
	kafkatest.Produce(t, topicAvailability, availabilityEvent("evt-c1", typeCreated, "sku-pub"))
	priceOnceKnown(t, s, "sku-pub", "k-create")
	msgs := kafkatest.ReadKeyed(t, topicEvents, "sku-pub", 1, 60*time.Second)
	if !strings.Contains(string(msgs[0].Value), "com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged") {
		t.Fatalf("published event = %s", msgs[0].Value)
	}
}

func TestRealWiringDiscontinuedItemWithdrawsItsPrice(t *testing.T) {
	s := bootReal(t, "withdraw")
	kafkatest.Produce(t, topicAvailability, availabilityEvent("evt-c2", typeCreated, "sku-wd"))
	priceOnceKnown(t, s, "sku-wd", "k-create-wd")
	kafkatest.Produce(t, topicAvailability, availabilityEvent("evt-d2", typeDiscontinued, "sku-wd"))
	eventually(t, 60*time.Second, "the price to be withdrawn", func() bool {
		return s.call(t, "GET", "/item-prices/sku-wd", "", "").field(t, "state") == "WITHDRAWN"
	})
	msgs := kafkatest.ReadKeyed(t, topicEvents, "sku-wd", 2, 60*time.Second)
	if !strings.Contains(string(msgs[1].Value), "ItemPriceWithdrawn") {
		t.Fatalf("second event = %s", msgs[1].Value)
	}
}

func TestRealWiringIssuesAQuoteAndPublishesItToAnalytics(t *testing.T) {
	s := bootReal(t, "quote")
	kafkatest.Produce(t, topicAvailability, availabilityEvent("evt-c3", typeCreated, "sku-q"))
	priceOnceKnown(t, s, "sku-q", "k-create-q")
	tiers := `[{"tier":"SAME_DAY","charge":%[1]s},{"tier":"NEXT_DAY","charge":%[1]s},{"tier":"TWO_DAY","charge":%[1]s},{"tier":"STANDARD","charge":%[1]s}]`
	if r := s.call(t, "POST", "/delivery-charge-schedules", "k-sched", fmt.Sprintf(`{"version":1,"tiers":`+tiers+`}`, money(500))); r.status != 201 {
		t.Fatalf("schedule: %d %s", r.status, r.body)
	}
	if r := s.call(t, "POST", "/tax-tables", "k-tax", `{"version":1,"bands":[{"postalPrefixFrom":"000","postalPrefixTo":"999","rateBasisPoints":1000}]}`); r.status != 201 {
		t.Fatalf("tax: %d %s", r.status, r.body)
	}
	q := s.call(t, "POST", "/price-quotes", "k-quote", `{"cartId":"cart-1","destinationPostalPrefix":"787","lines":[{"catalogItemId":"sku-q","quantity":2}],"groups":[{"groupNo":1,"offerId":"off-1","tier":"STANDARD","lineNos":[1]}]}`)
	if q.status != 201 {
		t.Fatalf("quote: %d %s", q.status, q.body)
	}
	msgs := kafkatest.ReadKeyed(t, topicAnalytics, q.field(t, "quoteId").(string), 1, 60*time.Second)
	if !strings.Contains(string(msgs[0].Value), "PriceQuoteIssued") {
		t.Fatalf("analytics event = %s", msgs[0].Value)
	}
}
