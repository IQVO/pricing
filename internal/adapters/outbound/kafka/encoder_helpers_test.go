package kafka_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/pricing/internal/adapters/outbound/kafka"
	"github.com/IQVO/pricing/internal/application/outbox"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/shared"
	"github.com/IQVO/pricing/internal/domain/tax"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var t0 = time.Date(2026, 10, 24, 9, 0, 0, 123456000, time.UTC)

const fixedID = "7a2c9e41-5f0b-4c3d-9e6a-1b8d4f0c2a55"

func changed() itemprice.ItemPriceChanged {
	return itemprice.ItemPriceChanged{
		Header: shared.Header{Version: 7, At: t0}, CatalogItemID: "cat-100234", Currency: "BRL",
		ListPriceMinor: 12990, EffectivePriceMinor: 9990, SaleEndsAt: time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC),
	}
}

func withdrawn() itemprice.ItemPriceWithdrawn {
	return itemprice.ItemPriceWithdrawn{Header: shared.Header{Version: 8, At: t0}, CatalogItemID: "cat-100234"}
}

func scheduleChanged() schedule.DeliveryChargeScheduleChanged {
	m := func(n int64) shared.Money { return shared.Money{AmountMinor: n, Currency: "BRL"} }
	return schedule.DeliveryChargeScheduleChanged{
		Header: shared.Header{Version: 3, At: t0}, ScheduleID: "sched-1",
		Tiers: []schedule.TierCharge{
			{Tier: shared.TierSameDay, Charge: m(2490)}, {Tier: shared.TierNextDay, Charge: m(1490), FreeOver: m(20000)},
			{Tier: shared.TierTwoDay, Charge: m(990)}, {Tier: shared.TierStandard, Charge: m(0), FreeOver: m(9900)},
		},
	}
}

func taxChanged() tax.TaxTableChanged {
	return tax.TaxTableChanged{
		Header: shared.Header{Version: 2, At: t0}, TaxTableID: "tax-1",
		Bands: []tax.TaxBand{{PostalPrefixFrom: "010", PostalPrefixTo: "099", RateBasisPoints: 1800}, {PostalPrefixFrom: "100", PostalPrefixTo: "199", RateBasisPoints: 0}},
	}
}

func quoteIssued() quote.PriceQuoteIssued {
	return quote.PriceQuoteIssued{
		Header: shared.Header{Version: 1, At: t0}, QuoteID: "q-1", CartID: "cart-9", Currency: "BRL",
		TotalMinor: 24570, ExpiresAt: t0.Add(15 * time.Minute),
	}
}

func encodeOne(t *testing.T, ev shared.Event) outbox.Message {
	t.Helper()
	msgs, err := (&kafka.Encoder{NewID: func() string { return fixedID }}).Encode(ev)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Encode: %v (%d messages)", err, len(msgs))
	}
	return msgs[0]
}

// assertGolden compares the CloudEvents bytes with the golden file EXACTLY.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, append(got, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("%s differs from its golden file:\n got %s\nwant %s", name, got, want)
	}
}

func assertEnvelope(t *testing.T, msg outbox.Message, entity, name, subject, topic string) {
	t.Helper()
	wantType := "com.retailnetwork.storefront.pricing." + entity + "." + name
	if msg.EventType != wantType || msg.EventID != fixedID || msg.Topic != topic {
		t.Fatalf("message = %+v", msg)
	}
	if msg.Subject != subject || string(msg.Key) != subject {
		t.Fatalf("subject/key = %q %q", msg.Subject, msg.Key)
	}
	stream := "events"
	if topic == kafka.TopicAnalytics {
		stream = "analytics"
	}
	if msg.DataSchema != "urn:retailnetwork:pricing:"+stream+":"+name+":v1" {
		t.Fatalf("dataschema = %q", msg.DataSchema)
	}
	if len(msg.Headers) != 1 || msg.Headers[0].Key != "content-type" || msg.Headers[0].Value != cloudevents.MediaType {
		t.Fatalf("headers = %+v", msg.Headers)
	}
	e, err := cloudevents.Decode(msg.Value)
	if err != nil || e.Type() != wantType || e.Source() != "/retailnetwork/pricing" || e.ID() != fixedID {
		t.Fatalf("decode: %v %v", err, e)
	}
}
