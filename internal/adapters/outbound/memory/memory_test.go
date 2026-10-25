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
	repocontract.Run(t, func(*testing.T) repocontract.Env {
		s := memory.NewStore(memory.NewClock(t0))
		return repocontract.Env{
			ItemPrices: s.ItemPrices(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Quotes: s.Quotes(),
			Catalog: s.Catalog(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(), Idempotency: s.Idempotency(),
			Clock:       memory.NewClock(t0),
			OutboxCount: func(*testing.T) int { return s.Counts().OutboxEvents },
		}
	})
}
