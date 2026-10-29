package kafka_test

import (
	"context"
	"errors"
	"testing"

	kafkaconsumer "github.com/IQVO/pricing/internal/adapters/inbound/kafka"
	"github.com/IQVO/pricing/internal/domain/itemprice"
)

func TestCreatedMakesTheItemActiveAndDuplicatesAreIgnored(t *testing.T) {
	r := newRig(t)
	msg := created(t, "evt-1", "sku-1")
	for i := 0; i < 2; i++ {
		if err := r.consumer.HandleMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.store.Catalog().State(context.Background(), "sku-1")
	if err != nil || got != itemprice.CatalogActive {
		t.Fatalf("state = %v, %v", got, err)
	}
	if c := r.store.Counts(); c.Catalog != 1 || c.Inbox != 1 {
		t.Fatalf("counts = %+v", c)
	}
}

func TestDiscontinuedWinsOverALateCreated(t *testing.T) {
	r := newRig(t)
	disc := message(t, "evt-2", kafkaconsumer.TypeCatalogItemDiscontinued, map[string]any{"catalogItemId": "sku-1"})
	for _, m := range [][]byte{disc, created(t, "evt-3", "sku-1")} {
		if err := r.consumer.HandleMessage(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := r.store.Catalog().State(context.Background(), "sku-1")
	if got != itemprice.CatalogDiscontinued {
		t.Fatalf("state = %v", got)
	}
}

func TestFullTypeDispatchIgnoresSuffixAndUnknownTypes(t *testing.T) {
	r := newRig(t)
	for _, typ := range []string{"CatalogItemCreated", "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemRenamed", "x." + kafkaconsumer.TypeCatalogItemCreated} {
		if err := r.consumer.HandleMessage(context.Background(), message(t, "e-"+typ, typ, map[string]any{"catalogItemId": "sku-1"})); err != nil {
			t.Fatal(err)
		}
	}
	if c := r.store.Counts(); c.Catalog != 0 || c.Inbox != 0 {
		t.Fatalf("counts = %+v", c)
	}
}

func TestLegacyFlatMessageIsSkipped(t *testing.T) {
	r := newRig(t)
	flat := []byte(`{"event_id":"1","event_type":"CatalogItemCreated","catalogItemId":"sku-1"}`)
	if err := r.consumer.HandleMessage(context.Background(), flat); err != nil {
		t.Fatal(err)
	}
	if c := r.store.Counts(); c.Catalog != 0 {
		t.Fatalf("counts = %+v", c)
	}
}

func TestPoisonPayloadsAreDeadLettered(t *testing.T) {
	for name, data := range map[string]any{"invalid id": map[string]any{"catalogItemId": "bad id!"}, "wrong shape": []int{1}} {
		r := newRig(t)
		err := r.consumer.HandleMessage(context.Background(), message(t, "e-poison", kafkaconsumer.TypeCatalogItemCreated, data))
		if err == nil || !errors.Is(err, kafkaconsumer.ErrPoison) {
			t.Fatalf("%s: err = %v, want poison", name, err)
		}
	}
}

func TestPoisonIsSkippedWithoutADeadLetterWriter(t *testing.T) {
	r := newRig(t)
	r.consumer.DLQ = nil
	err := r.consumer.HandleMessage(context.Background(), message(t, "e-p", kafkaconsumer.TypeCatalogItemCreated, map[string]any{"catalogItemId": ""}))
	if err != nil {
		t.Fatal(err)
	}
}
