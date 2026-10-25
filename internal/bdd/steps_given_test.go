package bdd

import (
	"context"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/pricing/internal/application/usecases"
)

func registerGiven(sc *godog.ScenarioContext, get func() *world) {
	sc.Step(`^the catalogue item "([^"]*)" is known$`, func(id string) error { return get().known(id) })
	sc.Step(`^the item "([^"]*)" is priced at (\d+)$`, func(id string, price int) error {
		w := get()
		if err := w.known(id); err != nil {
			return err
		}
		return w.expect(201, "POST", "/item-prices", fmt.Sprintf(`{"catalogItemId":%q,"listPrice":%s}`, id, money(price)))
	})
	sc.Step(`^every delivery tier charges (\d+) from version (\d+)$`, func(charge, version int) error {
		m := money(charge)
		tiers := fmt.Sprintf(`[{"tier":"SAME_DAY","charge":%[1]s},{"tier":"NEXT_DAY","charge":%[1]s},{"tier":"TWO_DAY","charge":%[1]s},{"tier":"STANDARD","charge":%[1]s}]`, m)
		return get().expect(201, "POST", "/delivery-charge-schedules", fmt.Sprintf(`{"version":%d,"tiers":%s}`, version, tiers))
	})
	sc.Step(`^a tax rate of (\d+) basis points everywhere from version (\d+)$`, func(rate, version int) error {
		body := fmt.Sprintf(`{"version":%d,"bands":[{"postalPrefixFrom":"000","postalPrefixTo":"ZZZ","rateBasisPoints":%d}]}`, version, rate)
		return get().expect(201, "POST", "/tax-tables", body)
	})
	sc.Step(`^(\d+) minutes pass$`, func(n int) error { get().clock.Advance(time.Duration(n) * time.Minute); return nil })
}

// known feeds CatalogItemCreated for the item, as the consumer would.
func (w *world) known(id string) error {
	w.seq++
	return (usecases.Consumer{Deps: w.deps}).CatalogItemCreated(context.Background(), fmt.Sprintf("ev-%d", w.seq), "created", id)
}

func money(n int) string { return fmt.Sprintf(`{"amountMinor":%d,"currency":"BRL"}`, n) }
