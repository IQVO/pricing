package kafka_test

import (
	"strings"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/adapters/outbound/kafka"
	"github.com/IQVO/pricing/internal/application/outbox"
	"github.com/IQVO/pricing/internal/domain/shared"
)

func TestItemPriceChangedGolden(t *testing.T) {
	msg := encodeOne(t, changed())
	assertGolden(t, "ItemPriceChanged", msg.Value)
	assertEnvelope(t, msg, "itemprice", "ItemPriceChanged", "cat-100234", kafka.TopicEvents)
}

func TestItemPriceChangedAtListPriceOmitsSaleEndsAt(t *testing.T) {
	ev := changed()
	ev.EffectivePriceMinor = ev.ListPriceMinor
	ev.SaleEndsAt = time.Time{}
	msg := encodeOne(t, ev)
	assertGolden(t, "ItemPriceChangedAtListPrice", msg.Value)
	if strings.Contains(string(msg.Value), "saleEndsAt") {
		t.Fatalf("saleEndsAt must be absent at list price: %s", msg.Value)
	}
}

func TestItemPriceWithdrawnGolden(t *testing.T) {
	msg := encodeOne(t, withdrawn())
	assertGolden(t, "ItemPriceWithdrawn", msg.Value)
	assertEnvelope(t, msg, "itemprice", "ItemPriceWithdrawn", "cat-100234", kafka.TopicEvents)
}

func TestDeliveryChargeScheduleChangedGolden(t *testing.T) {
	msg := encodeOne(t, scheduleChanged())
	assertGolden(t, "DeliveryChargeScheduleChanged", msg.Value)
	assertEnvelope(t, msg, "deliverychargeschedule", "DeliveryChargeScheduleChanged", "sched-1", kafka.TopicEvents)
}

func TestTaxTableChangedGolden(t *testing.T) {
	msg := encodeOne(t, taxChanged())
	assertGolden(t, "TaxTableChanged", msg.Value)
	assertEnvelope(t, msg, "taxtable", "TaxTableChanged", "tax-1", kafka.TopicEvents)
}

func TestPriceQuoteIssuedGoesToTheAnalyticsTopic(t *testing.T) {
	msg := encodeOne(t, quoteIssued())
	assertGolden(t, "PriceQuoteIssued", msg.Value)
	assertEnvelope(t, msg, "pricequote", "PriceQuoteIssued", "q-1", kafka.TopicAnalytics)
}

type otherEvent struct{ shared.Header }

func (otherEvent) EventName() string { return "Other" }
func (otherEvent) Entity() string    { return "other" }
func (otherEvent) SubjectID() string { return "x" }

func TestEncodeMintsAnIDPerEventAndKeepsOrder(t *testing.T) {
	msgs, err := kafka.NewEncoder().Encode(changed(), withdrawn(), quoteIssued())
	if err != nil || len(msgs) != 3 {
		t.Fatalf("Encode: %v (%d)", err, len(msgs))
	}
	if msgs[0].EventID == msgs[1].EventID || msgs[1].EventID == msgs[2].EventID || msgs[0].EventID == "" {
		t.Fatalf("ids must differ: %+v", msgs)
	}
	if !strings.HasSuffix(msgs[0].EventType, ".ItemPriceChanged") || !strings.HasSuffix(msgs[1].EventType, ".ItemPriceWithdrawn") || !strings.HasSuffix(msgs[2].EventType, ".PriceQuoteIssued") {
		t.Fatalf("order lost: %+v", msgs)
	}
}

func TestEncodeRefusesAnUnpublishedEvent(t *testing.T) {
	if _, err := kafka.NewEncoder().Encode(otherEvent{}); err == nil {
		t.Fatal("an event the service does not publish is a programming error")
	}
	msgs, err := (&kafka.Encoder{}).Encode()
	if err != nil || len(msgs) != 0 {
		t.Fatalf("no events encode to nothing: %v %v", msgs, err)
	}
}

func TestEncodeFailsOnAnEmptySubject(t *testing.T) {
	ev := withdrawn()
	ev.CatalogItemID = ""
	if _, err := kafka.NewEncoder().Encode(ev); err == nil {
		t.Fatal("an event without a subject cannot be keyed")
	}
}

func TestEncoderPrefixIsolatesTopicsAndZeroEncoderMintsUUIDs(t *testing.T) {
	msgs, err := (&kafka.Encoder{Prefix: "t1."}).Encode(changed(), quoteIssued())
	if err != nil || msgs[0].Topic != "t1."+kafka.TopicEvents || msgs[1].Topic != "t1."+kafka.TopicAnalytics || len(msgs[0].EventID) != 36 {
		t.Fatalf("Encode: %v %+v", err, msgs)
	}
}

func TestHeadersConvertToKafkaHeaders(t *testing.T) {
	got := kafka.Headers([]outbox.Header{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}})
	if len(got) != 2 || got[0].Key != "a" || string(got[0].Value) != "1" || got[1].Key != "b" || string(got[1].Value) != "2" {
		t.Fatalf("headers = %+v", got)
	}
}
