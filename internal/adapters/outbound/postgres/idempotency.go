package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/pricing/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/pricing/internal/application/idempotency"
	"github.com/IQVO/pricing/internal/application/ports"
)

// IdempotencyStore is the Postgres ports.IdempotencyStore over idempotency_keys.
// The key is global (REST guideline 2.3): every row carries the empty route, so
// the (route, key) primary key is a unique index on the key alone.
type IdempotencyStore struct{ pool *pgxpool.Pool }

var _ ports.IdempotencyStore = (*IdempotencyStore)(nil)

// NewIdempotencyStore constructs an IdempotencyStore over pool.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore { return &IdempotencyStore{pool: pool} }

func ttlSeconds() float64 { return idempotency.TTL.Seconds() }

// claimAttempts bounds how often Do re-claims a key whose live row vanished
// between the failed claim and the read (an expiry race).
const claimAttempts = 3

// httpBadRequest is the first status that is an error answer (a 4xx).
const httpBadRequest = 400

// httpServerError is the first status that is never stored (a 5xx).
const httpServerError = 500

// claimSQL inserts the key as a placeholder inside the request's transaction. A
// concurrent claimant of the same key blocks on the unique index until this
// transaction ends: it then finds the committed answer (no row is affected) or,
// after a rollback, claims the key itself. An expired row is taken over.
const claimSQL = `
	INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at)
	VALUES ('', $1, $2, 0, '', ''::bytea, $3)
	ON CONFLICT (route, key) DO UPDATE SET request_hash = EXCLUDED.request_hash, status = 0,
		content_type = '', body = ''::bytea, created_at = EXCLUDED.created_at
	WHERE idempotency_keys.created_at <= now() - make_interval(secs => $4)`

const storeSQL = `UPDATE idempotency_keys SET status = $2, content_type = $3, body = $4 WHERE route = '' AND key = $1`

const readSQL = `
	SELECT request_hash, status, content_type, body, created_at FROM idempotency_keys
	WHERE route = '' AND key = $1 AND created_at > now() - make_interval(secs => $2)`

// purgeSQL deletes one bounded batch of the rows the read check above already
// ignores: created_at <= now() - TTL is the exact complement of readSQL's
// created_at > now() - TTL, on the same ttlSeconds() age (idempotency.TTL is
// the one age of the read check, the claim takeover and this purge). The rows
// are picked FOR UPDATE SKIP LOCKED, so a row a request holds (its claim took
// the expired row over and refreshes created_at under the row lock until it
// commits) is skipped, never waited for, and the age test runs on the locked
// rows, so a row a request just refreshed is never picked. It takes no
// advisory lock and never blocks a request: an expired row is invisible to Do,
// so deleting it is not observable.
const purgeSQL = `DELETE FROM idempotency_keys
	WHERE (route, key) IN (
		SELECT route, key FROM idempotency_keys
		WHERE created_at <= now() - make_interval(secs => $1)
		ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)`

// Do implements ports.IdempotencyStore: the claim, fn's writes (through the
// transaction carried by the ctx it is handed, so every unit of work joins it)
// and the stored answer commit together.
//
// fn runs behind a SAVEPOINT. A mapped domain conflict -- a request losing a
// race on a unique index, say -- fails a statement on the server and leaves the
// transaction ABORTED even though fn answers a truthful 4xx. Releasing the
// savepoint exposes that: the release fails, ROLLBACK TO SAVEPOINT discards fn's
// half-done work and heals the transaction, and the 4xx outcome is stored and
// committed with the claim like any other. A 2xx answer behind a failed release
// is a lie (its writes are gone), so it rolls everything back and fails the
// request instead, and the key retries the real work.
func (s *IdempotencyStore) Do(ctx context.Context, key, requestHash string, at time.Time,
	fn func(ctx context.Context) (idempotency.Record, error),
) (idempotency.Record, bool, error) {
	for range claimAttempts {
		rec, replayed, retry, err := s.attempt(ctx, key, requestHash, at, fn)
		if !retry {
			return rec, replayed, err
		}
	}
	return idempotency.Record{}, false, fmt.Errorf("claim idempotency key: the stored record kept expiring under the claim")
}

func (s *IdempotencyStore) attempt(ctx context.Context, key, requestHash string, at time.Time,
	fn func(ctx context.Context) (idempotency.Record, error),
) (rec idempotency.Record, replayed, retry bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return rec, false, false, fmt.Errorf("begin idempotent request: %w", err)
	}
	// A rollback after Commit is a harmless no-op; it also covers a panic in fn.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, claimSQL, key, requestHash, at.UTC(), ttlSeconds())
	if err != nil {
		return rec, false, false, fmt.Errorf("claim idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.replay(ctx, key)
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT handler_run"); err != nil {
		return idempotency.Record{}, false, false, fmt.Errorf("savepoint idempotent request: %w", err)
	}
	if rec, err = fn(pgtx.With(ctx, tx)); err != nil {
		return idempotency.Record{}, false, false, err
	}
	if rec.Release || rec.Status >= httpServerError {
		// rolled back by the defer, key row included: a released answer (the
		// middleware marks every answer that is not a 2xx and not FINAL or
		// OUTCOME, ADR 0011) and a 5xx are never stored, so the key runs again.
		return rec, false, false, nil
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT handler_run"); err != nil {
		// A statement inside fn failed on the server: the transaction is
		// aborted. Rolling back to the savepoint discards fn's partial writes
		// (all-or-nothing for an error answer) and returns the transaction to
		// health; the claim made before the savepoint survives.
		if _, rerr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT handler_run"); rerr != nil {
			return idempotency.Record{}, false, false, fmt.Errorf("heal idempotent request: %w", errors.Join(err, rerr))
		}
		if rec.Status < httpBadRequest {
			// A success whose writes cannot be committed must neither be
			// returned nor stored: fail so the key retries (the defer rolls back).
			return idempotency.Record{}, false, false, fmt.Errorf("release idempotent request savepoint: %w", err)
		}
	}
	body := rec.Body
	if body == nil {
		body = []byte{}
	}
	if _, err := tx.Exec(ctx, storeSQL, key, rec.Status, rec.ContentType, body); err != nil {
		return idempotency.Record{}, false, false, fmt.Errorf("store idempotency key: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return idempotency.Record{}, false, false, fmt.Errorf("commit idempotent request: %w", err)
	}
	rec.RequestHash, rec.CreatedAt = requestHash, at.UTC()
	return rec, false, false, nil
}

// replay reads the committed record that made the claim fail; it asks for a new
// claim when the record expired or was removed in between.
func (s *IdempotencyStore) replay(ctx context.Context, key string) (rec idempotency.Record, replayed, retry bool, err error) {
	err = s.pool.QueryRow(ctx, readSQL, key, ttlSeconds()).
		Scan(&rec.RequestHash, &rec.Status, &rec.ContentType, &rec.Body, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.Record{}, false, true, nil
	}
	if err != nil {
		return idempotency.Record{}, false, false, fmt.Errorf("read idempotency key: %w", err)
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, true, false, nil
}

// PurgeExpired implements ports.IdempotencyStore: it deletes at most limit
// rows older than idempotency.TTL, oldest first (idempotency_keys_created_idx),
// and returns how many. A result below limit means nothing expired is left.
func (s *IdempotencyStore) PurgeExpired(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, purgeSQL, ttlSeconds(), limit)
	if err != nil {
		return 0, fmt.Errorf("idempotency purge: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
