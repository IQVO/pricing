package ids

import (
	"regexp"
	"testing"

	"github.com/IQVO/pricing/internal/domain/shared"
)

func TestIDsAreValidAndUnique(t *testing.T) {
	rx := regexp.MustCompile(`^(sale|sched|tax|quote)-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	g := Generator{}
	for i := 0; i < 200; i++ {
		for _, id := range []string{g.NewSaleID(), string(g.NewScheduleID()), string(g.NewTaxTableID()), string(g.NewQuoteID())} {
			if !rx.MatchString(id) || !shared.ValidID(id) || seen[id] {
				t.Fatalf("id %q is malformed or repeated", id)
			}
			seen[id] = true
		}
	}
}
