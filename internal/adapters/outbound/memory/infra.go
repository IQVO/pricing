package memory

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/IQVO/pricing/internal/application/idempotency"
)

// UnitOfWork is the in-memory ports.UnitOfWork: serialised, atomic, re-entrant.
type UnitOfWork struct{ s *Store }

// Do runs fn holding the store. When fn returns an error or panics the state
// taken before fn is restored; the error is returned unchanged. A Do inside a
// Do joins it but is atomic on its own (the state taken before the inner fn is
// restored when only the inner fn fails), like the Postgres savepoint.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if ctx.Value(txKey{u.s}) != nil {
		before := u.s.capture()
		if err = fn(ctx); err != nil {
			u.s.restore(before)
		}
		return err
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	before := u.s.capture()
	committed := false
	defer func() {
		if !committed {
			u.s.restore(before)
		}
	}()
	err = fn(context.WithValue(ctx, txKey{u.s}, true))
	committed = err == nil
	return err
}

// IdempotencyStore is the in-memory ports.IdempotencyStore. Records older than
// idempotency.TTL are treated as absent.
type IdempotencyStore struct{ s *Store }

// Do implements ports.IdempotencyStore. Like a unit of work it holds the store
// while fn runs, so same-key requests are serialised and a failed or 5xx fn
// leaves no write behind.
func (i *IdempotencyStore) Do(ctx context.Context, key, requestHash string, at time.Time,
	fn func(ctx context.Context) (idempotency.Record, error),
) (idempotency.Record, bool, error) {
	i.s.mu.Lock()
	defer i.s.mu.Unlock()
	if old, ok := i.s.idempotency[key]; ok && i.s.now().Sub(old.CreatedAt) <= idempotency.TTL {
		return old, true, nil
	}
	before := i.s.capture()
	stored := false
	defer func() {
		if !stored {
			i.s.restore(before)
		}
	}()
	rec, err := fn(context.WithValue(ctx, txKey{i.s}, true))
	if err != nil {
		return idempotency.Record{}, false, err
	}
	if rec.Release || rec.Status >= http.StatusInternalServerError {
		// rolled back by the deferred restore: a released answer and a 5xx are
		// never stored, so the key runs again (ADR 0011).
		return rec, false, nil
	}
	rec.RequestHash, rec.CreatedAt = requestHash, at
	i.s.idempotency[key] = rec
	stored = true
	return rec, false, nil
}

// PurgeExpired implements ports.IdempotencyStore: it deletes at most limit
// records older than idempotency.TTL — the exact complement of Do's live check
// on the same clock — oldest first, and returns how many. Do holds the same
// mutex while it runs fn, so a purge waits for a request in flight and then
// finds its answer live; nothing a request can still replay is ever deleted.
func (i *IdempotencyStore) PurgeExpired(_ context.Context, limit int) (int, error) {
	i.s.mu.Lock()
	defer i.s.mu.Unlock()
	var expired []string
	for key, rec := range i.s.idempotency {
		if i.s.now().Sub(rec.CreatedAt) > idempotency.TTL {
			expired = append(expired, key)
		}
	}
	sort.Slice(expired, func(a, b int) bool {
		return i.s.idempotency[expired[a]].CreatedAt.Before(i.s.idempotency[expired[b]].CreatedAt)
	})
	if limit < len(expired) {
		expired = expired[:max(limit, 0)]
	}
	for _, key := range expired {
		delete(i.s.idempotency, key)
	}
	return len(expired), nil
}
