package bdd

import (
	"fmt"
	"net/url"

	"github.com/cucumber/godog"
)

// steps_wave2_test.go — the wave-2 addition: following a remembered cursor
// (the item-price list pages by keyset).
func registerWave2(sc *godog.ScenarioContext, get func() *world) {
	sc.Step(`^the API gets "([^"]*)" with the remembered cursor "([^"]*)"$`, func(path, name string) error {
		w := get()
		cursor, ok := w.saved[name]
		if !ok || cursor == "" {
			return fmt.Errorf("no remembered cursor %q (saved: %v)", name, w.saved)
		}
		return w.send("GET", path+"?cursor="+url.QueryEscape(cursor), "", "")
	})
}
