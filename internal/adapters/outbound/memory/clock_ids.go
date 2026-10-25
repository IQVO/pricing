package memory

import (
	"fmt"
	"sync"
	"time"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/quote"
	"github.com/IQVO/pricing/internal/domain/schedule"
	"github.com/IQVO/pricing/internal/domain/tax"
)

var (
	_ ports.Clock       = (*Clock)(nil)
	_ ports.IDGenerator = (*SequentialIDs)(nil)
)

// Clock is a settable ports.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock reading t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the current reading.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// SequentialIDs is a ports.IDGenerator minting sale-1, sale-2, ..., schedule-1,
// tax-1 and quote-1 ... in order.
type SequentialIDs struct {
	mu                          sync.Mutex
	sales, schedules, tables, q int
}

// NewSaleID returns the next sale id.
func (g *SequentialIDs) NewSaleID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sales++
	return fmt.Sprintf("sale-%d", g.sales)
}

// NewScheduleID returns the next schedule id.
func (g *SequentialIDs) NewScheduleID() schedule.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.schedules++
	return schedule.ID(fmt.Sprintf("schedule-%d", g.schedules))
}

// NewTaxTableID returns the next tax table id.
func (g *SequentialIDs) NewTaxTableID() tax.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tables++
	return tax.ID(fmt.Sprintf("tax-%d", g.tables))
}

// NewQuoteID returns the next quote id.
func (g *SequentialIDs) NewQuoteID() quote.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.q++
	return quote.ID(fmt.Sprintf("quote-%d", g.q))
}
