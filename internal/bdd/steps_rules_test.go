package bdd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/pricing/internal/application/usecases"
	"github.com/IQVO/pricing/internal/domain/itemprice"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// steps_rules_test.go — wave 3: the vocabulary of the documentation-derived
// scenarios (features/*_rules.feature): bulk fixtures for the documented caps,
// event inspection through the outbox double, redelivery of consumed events,
// the readiness gate and a few finer clock steps.
func registerRules(sc *godog.ScenarioContext, get func() *world) {
	registerRulesGiven(sc, get)
	registerRulesWhen(sc, get)
	registerRulesThen(sc, get)
}

func registerRulesGiven(sc *godog.ScenarioContext, get func() *world) {
	sc.Step(`^the API accepts a POST to "([^"]*)"(?: with)? the body:$`, func(path string, body *godog.DocString) error {
		return get().expect(201, "POST", path, body.Content)
	})
	sc.Step(`^(\d+) items named "([^"]*)" numbered from 1 are priced at (\d+)$`, func(n int, name string, price int) error {
		w := get()
		for i := 1; i <= n; i++ {
			id := fmt.Sprintf("%s-%d", name, i)
			if err := w.known(id); err != nil {
				return err
			}
			if err := w.expect(201, "POST", "/item-prices", fmt.Sprintf(`{"catalogItemId":%q,"listPrice":%s}`, id, money(price))); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the item "([^"]*)" has (\d+) future one-day sales priced at (\d+)$`, func(id string, n, price int) error {
		w := get()
		for day := 1; day <= n; day++ {
			if err := w.expect(201, "POST", "/item-prices/"+id+"/sales", saleOnDay(price, day)); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^(\d+) seconds pass$`, func(n int) error { get().clock.Advance(time.Duration(n) * time.Second); return nil })
	sc.Step(`^(\d+) hours pass$`, func(n int) error { get().clock.Advance(time.Duration(n) * time.Hour); return nil })
	sc.Step(`^Postgres is unreachable$`, func() error { get().pgDown.Store(true); return nil })
	sc.Step(`^Kafka is unreachable$`, func() error { get().kafkaDown.Store(true); return nil })
	sc.Step(`^Postgres answers again$`, func() error { get().pgDown.Store(false); return nil })
	sc.Step(`^Kafka answers again$`, func() error { get().kafkaDown.Store(false); return nil })
	sc.Step(`^shutdown has started$`, func() error { get().ready.SetNotReady(); return nil })
}

// saleOnDay is a one-day sale window; day 1 is 2026-11-01.
func saleOnDay(price, day int) string {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, day-1)
	return fmt.Sprintf(`{"price":%s,"from":%q,"to":%q}`, money(price), from.Format(time.RFC3339), from.AddDate(0, 0, 1).Format(time.RFC3339))
}

func registerRulesWhen(sc *godog.ScenarioContext, get func() *world) {
	sc.Step(`^the API schedules a one-day sale for "([^"]*)" priced at (\d+) on day (\d+)$`, func(id string, price, day int) error {
		w := get()
		w.seq++
		return w.send("POST", "/item-prices/"+id+"/sales", fmt.Sprintf("auto-%d", w.seq), saleOnDay(price, day))
	})
	sc.Step(`^the item whose id has (\d+) characters is priced at (\d+)$`, func(n, price int) error {
		w := get()
		id := strings.Repeat("a", n)
		_ = w.known(id) // an id longer than the domain holds is refused by the consumer: the price must refuse it too
		w.seq++
		return w.send("POST", "/item-prices", fmt.Sprintf("auto-%d", w.seq), fmt.Sprintf(`{"catalogItemId":%q,"listPrice":%s}`, id, money(price)))
	})
	sc.Step(`^the API publishes a charge schedule of version (\d+) naming the tiers "([^"]*)"$`, func(version int, tiers string) error {
		var parts []string
		for _, t := range strings.Split(tiers, ", ") {
			if t != "" {
				parts = append(parts, fmt.Sprintf(`{"tier":%q,"charge":%s}`, t, money(500)))
			}
		}
		w := get()
		w.seq++
		return w.send("POST", "/delivery-charge-schedules", fmt.Sprintf("auto-%d", w.seq), fmt.Sprintf(`{"version":%d,"tiers":[%s]}`, version, strings.Join(parts, ",")))
	})
	sc.Step(`^the API publishes a tax table of version (\d+) with (\d+) one-prefix bands of (\d+) basis points$`, func(version, n, rate int) error {
		bands := make([]string, n)
		for i := range bands {
			p := fmt.Sprintf("%03d", i)
			bands[i] = fmt.Sprintf(`{"postalPrefixFrom":%q,"postalPrefixTo":%q,"rateBasisPoints":%d}`, p, p, rate)
		}
		w := get()
		w.seq++
		return w.send("POST", "/tax-tables", fmt.Sprintf("auto-%d", w.seq), fmt.Sprintf(`{"version":%d,"bands":[%s]}`, version, strings.Join(bands, ",")))
	})
	sc.Step(`^the API issues a quote for the items "([^"]*)" numbered 1 to (\d+) in groups of (\d+) to postal prefix "([^"]*)"$`, func(name string, n, per int, prefix string) error {
		var lines, groups []string
		for i := 1; i <= n; i++ {
			lines = append(lines, fmt.Sprintf(`{"catalogItemId":"%s-%d","quantity":1}`, name, i))
		}
		for g, first := 1, 1; first <= n; g, first = g+1, first+per {
			var nos []string
			for no := first; no < first+per && no <= n; no++ {
				nos = append(nos, fmt.Sprint(no))
			}
			groups = append(groups, fmt.Sprintf(`{"groupNo":%d,"offerId":"offer-%d","tier":"STANDARD","lineNos":[%s]}`, g, g, strings.Join(nos, ",")))
		}
		w := get()
		w.seq++
		body := fmt.Sprintf(`{"cartId":"cart-bulk","destinationPostalPrefix":%q,"lines":[%s],"groups":[%s]}`, prefix, strings.Join(lines, ","), strings.Join(groups, ","))
		return w.send("POST", "/price-quotes", fmt.Sprintf("auto-%d", w.seq), body)
	})
	sc.Step(`^the API gets the quote remembered as "([^"]*)"$`, func(name string) error {
		w := get()
		id, ok := w.saved[name]
		if !ok || id == "" {
			return fmt.Errorf("no remembered quote %q (saved: %v)", name, w.saved)
		}
		return w.send("GET", "/price-quotes/"+id, "", "")
	})
	sc.Step(`^the API posts to "([^"]*)" a body of (\d+) bytes$`, func(path string, n int) error {
		const base = `{"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}`
		if n < len(base) {
			return fmt.Errorf("a body of %d bytes cannot hold the %d bytes of the request", n, len(base))
		}
		w := get()
		w.seq++
		return w.send("POST", path, fmt.Sprintf("auto-%d", w.seq), base+strings.Repeat(" ", n-len(base)))
	})
	sc.Step(`^offer-availability delivers the event "([^"]*)" that creates the item "([^"]*)"$`, func(eventID, id string) error {
		w := get()
		w.lastErr = (usecases.Consumer{Deps: w.deps}).CatalogItemCreated(context.Background(), eventID, "created", id)
		return nil
	})
	sc.Step(`^offer-availability delivers the event "([^"]*)" that discontinues the item "([^"]*)"$`, func(eventID, id string) error {
		w := get()
		w.lastErr = (usecases.Consumer{Deps: w.deps}).CatalogItemDiscontinued(context.Background(), eventID, "discontinued", id)
		return nil
	})
}

func registerRulesThen(sc *godog.ScenarioContext, get func() *world) {
	sc.Step(`^the consumer acknowledged the event$`, func() error { return get().wantConsumerAck() })
	sc.Step(`^the consumer refused the event as invalid$`, func() error { return get().wantConsumerRefusal() })
	sc.Step(`^the response is not marked replayed$`, func() error { return get().wantNotReplayed() })
	sc.Step(`^the response has no field "([^"]*)"$`, func(path string) error { return get().wantNoField(path) })
	sc.Step(`^the response list "([^"]*)" has (\d+) items$`, func(path string, n int) error { return get().wantListLen(path, n) })
	sc.Step(`^(\d+) events were published in total$`, func(n int) error {
		if got := len(get().store.Events()); got != n {
			return fmt.Errorf("%d events in total, want %d", got, n)
		}
		return nil
	})
	sc.Step(`^the store holds (\d+) (item prices|schedules|tax tables|quotes)$`, func(n int, what string) error { return get().wantStoreCount(n, what) })
	sc.Step(`^the stored quote "([^"]*)" is (ISSUED|EXPIRED)$`, func(id, state string) error { return get().wantStoredQuote(id, state) })
	registerEventThen(sc, get)
}

func (w *world) wantConsumerAck() error {
	if w.lastErr != nil {
		return fmt.Errorf("the consumer failed: %w", w.lastErr)
	}
	return nil
}

func (w *world) wantConsumerRefusal() error {
	if !errors.Is(w.lastErr, usecases.ErrInvalidEvent) {
		return fmt.Errorf("consumer error = %w, want ErrInvalidEvent", w.lastErr)
	}
	return nil
}

func (w *world) wantNotReplayed() error {
	if v := w.header["Idempotency-Replayed"]; len(v) != 0 {
		return fmt.Errorf("Idempotency-Replayed = %v, want none", v)
	}
	return nil
}

func (w *world) wantNoField(path string) error {
	got, err := w.field(path)
	if err == nil && got != "<nil>" {
		return fmt.Errorf("%s = %q, want it absent", path, got)
	}
	return nil
}

func (w *world) wantListLen(path string, n int) error {
	var cur any
	if err := json.Unmarshal([]byte(w.body), &cur); err != nil {
		return fmt.Errorf("body is not JSON: %q", w.body)
	}
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return fmt.Errorf("no field %q in %s", path, w.body)
		}
		cur = m[part]
	}
	list, ok := cur.([]any)
	if !ok || len(list) != n {
		return fmt.Errorf("%s = %v, want a list of %d items", path, cur, n)
	}
	return nil
}

func (w *world) wantStoreCount(n int, what string) error {
	c := w.store.Counts()
	got := map[string]int{"item prices": c.ItemPrices, "schedules": c.Schedules, "tax tables": c.TaxTables, "quotes": c.Quotes}[what]
	if got != n {
		return fmt.Errorf("the store holds %d %s, want %d", got, what, n)
	}
	return nil
}

func (w *world) wantStoredQuote(id, state string) error {
	q, err := w.store.Quotes().Get(context.Background(), quote.ID(id))
	if err != nil {
		return err
	}
	if string(q.State()) != state {
		return fmt.Errorf("stored quote %s is %s, want %s", id, q.State(), state)
	}
	return nil
}

func registerEventThen(sc *godog.ScenarioContext, get func() *world) {
	sc.Step(`^the latest "ItemPriceChanged" event has list price (\d+) and effective price (\d+)$`, func(list, eff int) error {
		return get().wantPriceChanged(func(e itemprice.ItemPriceChanged) error {
			if e.ListPriceMinor != int64(list) || e.EffectivePriceMinor != int64(eff) {
				return fmt.Errorf("latest ItemPriceChanged list %d effective %d, want %d and %d", e.ListPriceMinor, e.EffectivePriceMinor, list, eff)
			}
			return nil
		})
	})
	sc.Step(`^the latest "ItemPriceChanged" event has its sale ending at "([^"]*)"$`, func(at string) error {
		want, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return err
		}
		return get().wantPriceChanged(func(e itemprice.ItemPriceChanged) error {
			if !e.SaleEndsAt.Equal(want) {
				return fmt.Errorf("saleEndsAt = %v, want %v", e.SaleEndsAt, want)
			}
			return nil
		})
	})
	sc.Step(`^the latest "ItemPriceChanged" event has no sale end$`, func() error {
		return get().wantPriceChanged(func(e itemprice.ItemPriceChanged) error {
			if !e.SaleEndsAt.IsZero() {
				return fmt.Errorf("saleEndsAt = %v, want none", e.SaleEndsAt)
			}
			return nil
		})
	})
	sc.Step(`^the "([^"]*)" events carry versions "([^"]*)"$`, func(name, want string) error { return get().wantVersions(name, want) })
	sc.Step(`^the latest "([^"]*)" event carries version (\d+) and is keyed by "([^"]*)"$`, func(name string, version int, key string) error {
		e, err := latestEvent(get(), name)
		if err != nil {
			return err
		}
		if e.AggregateVersion() != int64(version) || e.SubjectID() != key {
			return fmt.Errorf("latest %s has version %d and key %q, want %d and %q", name, e.AggregateVersion(), e.SubjectID(), version, key)
		}
		return nil
	})
	sc.Step(`^the latest "PriceQuoteIssued" event totals (\d+) for the cart "([^"]*)" and expires at "([^"]*)"$`, func(total int, cart, at string) error {
		return get().wantQuoteIssued(int64(total), cart, at)
	})
}

func (w *world) wantVersions(name, want string) error {
	var got []string
	for _, e := range w.store.Events() {
		if e.EventName() == name {
			got = append(got, fmt.Sprint(e.AggregateVersion()))
		}
	}
	if strings.Join(got, ",") != want {
		return fmt.Errorf("%s versions = %s, want %s", name, strings.Join(got, ","), want)
	}
	return nil
}

func (w *world) wantQuoteIssued(total int64, cart, at string) error {
	e, err := latestEvent(w, "PriceQuoteIssued")
	if err != nil {
		return err
	}
	q, ok := e.(quote.PriceQuoteIssued)
	if !ok {
		return fmt.Errorf("event is %T, not a PriceQuoteIssued", e)
	}
	want, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return err
	}
	if q.TotalMinor != total || q.CartID != cart || !q.ExpiresAt.Equal(want) {
		return fmt.Errorf("PriceQuoteIssued total %d cart %q expires %v, want %d %q %v", q.TotalMinor, q.CartID, q.ExpiresAt, total, cart, want)
	}
	return nil
}

// wantPriceChanged runs check on the latest ItemPriceChanged event.
func (w *world) wantPriceChanged(check func(itemprice.ItemPriceChanged) error) error {
	e, err := latestEvent(w, "ItemPriceChanged")
	if err != nil {
		return err
	}
	c, ok := e.(itemprice.ItemPriceChanged)
	if !ok {
		return fmt.Errorf("event is %T, not an ItemPriceChanged", e)
	}
	return check(c)
}

func latestEvent(w *world, name string) (shared.Event, error) {
	events := w.store.Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventName() == name {
			return events[i], nil
		}
	}
	return nil, fmt.Errorf("no %s event was published", name)
}
