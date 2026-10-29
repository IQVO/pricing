// Package ids is the production ports.IDGenerator: every id is a prefix and 12
// random hexadecimal characters.
package ids

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/tax"
)

// Generator mints ids.
type Generator struct{}

var _ ports.IDGenerator = Generator{}

func random(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("ids: system randomness is unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

// NewSaleID returns "sale-" and 12 hexadecimal characters.
func (Generator) NewSaleID() string { return random("sale-") }

// NewScheduleID returns "sched-" and 12 hexadecimal characters.
func (Generator) NewScheduleID() schedule.ID { return schedule.ID(random("sched-")) }

// NewTaxTableID returns "tax-" and 12 hexadecimal characters.
func (Generator) NewTaxTableID() tax.ID { return tax.ID(random("tax-")) }

// NewQuoteID returns "quote-" and 12 hexadecimal characters.
func (Generator) NewQuoteID() quote.ID { return quote.ID(random("quote-")) }
