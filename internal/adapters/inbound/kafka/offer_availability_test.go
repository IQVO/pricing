package kafka_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	kafkaconsumer "github.com/IQVO/pricing/internal/adapters/inbound/kafka"
	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
	"github.com/IQVO/pricing/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

type rig struct {
	store    *memory.Store
	consumer *kafkaconsumer.Consumer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	clock := memory.NewClock(t0)
	s := memory.NewStore(clock)
	deps := usecases.Deps{
		ItemPrices: s.ItemPrices(), Catalog: s.Catalog(), Inbox: s.Inbox(), Outbox: s.Outbox(),
		UoW: s.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "BRL",
	}
	c := &kafkaconsumer.Consumer{
		Name: "offer-availability", Topic: kafkaconsumer.OfferAvailabilityTopic,
		Handlers:          kafkaconsumer.OfferAvailabilityHandlers(usecases.Consumer{Deps: deps}),
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		DeadLetterInvalid: true, DLQ: noDLQ{},
	}
	return &rig{store: s, consumer: c}
}

type noDLQ struct{}

func (noDLQ) WriteMessages(context.Context, ...kafkago.Message) error { return nil }
func (noDLQ) Close() error                                            { return nil }

// message builds a structured-mode CloudEvent of the given type.
func message(t *testing.T, id, typ string, data any) []byte {
	t.Helper()
	e := ce.New()
	e.SetID(id)
	e.SetSource("/retailnetwork/offer-availability")
	e.SetType(typ)
	e.SetSubject("sku-1")
	e.SetTime(t0)
	if err := e.SetData(ce.ApplicationJSON, data); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func created(t *testing.T, id, item string) []byte {
	return message(t, id, kafkaconsumer.TypeCatalogItemCreated, map[string]any{"catalogItemId": item, "title": "ignored"})
}
