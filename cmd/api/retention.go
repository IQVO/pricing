package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/IQVO/pricing/internal/application/idempotency"
)

// The idempotency_keys retention (ADR 0011, addendum 2026-11-22): every
// idempotencyPurgeInterval the purge deletes the rows older than
// idempotency.TTL, at most idempotencyPurgeBatch per statement, so one pass
// never holds a long lock however far behind it is. The two values are fixed
// on purpose (like the retention of the sibling services): the age is
// idempotency.TTL — expiry is already enforced at read, the purge only stops
// the table from growing — and the rest is housekeeping, not configuration.
const (
	idempotencyPurgeInterval = time.Hour
	idempotencyPurgeBatch    = 1000
)

// purgeStore is the retention half of ports.IdempotencyStore the loop needs
// (the port itself: postgres.IdempotencyStore). The narrow type keeps the
// loop testable with a fake.
type purgeStore interface {
	PurgeExpired(ctx context.Context, limit int) (deleted int, err error)
}

// purgeIdempotencyKeys runs batches until one comes back short (the backlog is
// gone) or ctx ends, and returns how many rows it deleted.
func purgeIdempotencyKeys(ctx context.Context, store purgeStore, batch int) (int, error) {
	total := 0
	for ctx.Err() == nil {
		n, err := store.PurgeExpired(ctx, batch)
		total += n
		if err != nil || n < batch {
			return total, err
		}
	}
	return total, nil
}

// purgeLoop purges at once and then every interval until ctx ends. A failed
// pass is logged and retried at the next tick: the purge is idempotent, and a
// row that outlives its TTL is never replayed, so a failure costs only disk.
func purgeLoop(ctx context.Context, logger *slog.Logger, store purgeStore, interval time.Duration, batch int) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		switch n, err := purgeIdempotencyKeys(ctx, store, batch); {
		case err != nil && ctx.Err() == nil:
			logger.Error("idempotency purge failed", "deleted", n, "error", sanitizeErr(err))
		case n > 0:
			logger.Info("idempotency purge done", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// startIdempotencyPurge starts the always-on retention loop, like the relay
// and the policies; it ends only when the returned stop is called.
func startIdempotencyPurge(store purgeStore, logger *slog.Logger) (*worker, func()) {
	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "idempotency-purge", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		logger.Info("idempotency purge running", "interval", idempotencyPurgeInterval, "batch", idempotencyPurgeBatch, "ttl", idempotency.TTL)
		purgeLoop(ctx, logger, store, idempotencyPurgeInterval, idempotencyPurgeBatch)
	}()
	return w, stop
}
