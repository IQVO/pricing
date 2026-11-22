package memory_test

import (
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
	"github.com/IQVO/pricing/internal/adapters/outbound/repocontract"
)

// t0 is an arbitrary FIXED instant: the store judges TTL on the injected
// clock, never on the wall, so this date never goes stale.
var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

func TestMemoryDoublesHonourTheRepositoryContract(t *testing.T) {
	newEnv := func(*testing.T) repocontract.Env {
		// ONE clock backs the store and the suite: the double judges TTL with
		// the store's clock, so Age (which advances it) moves every record's
		// age at once.
		clk := memory.NewClock(t0)
		s := memory.NewStore(clk)
		return repocontract.Env{
			ItemPrices: s.ItemPrices(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Quotes: s.Quotes(),
			Catalog: s.Catalog(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(), Idempotency: s.Idempotency(),
			Clock:       clk,
			Age:         func(_ *testing.T, d time.Duration) { clk.Advance(d) },
			OutboxCount: func(*testing.T) int { return s.Counts().OutboxEvents },
		}
	}
	repocontract.Run(t, newEnv)
	repocontract.RunIdempotencyPurge(t, newEnv)
}
