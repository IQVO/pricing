package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// fakePurger answers PurgeExpired from a script (the last answer repeats),
// records the limits it was called with, and can fail or act once per call.
type fakePurger struct {
	script  []int
	errAt   int // a call index that answers errBoom instead (0 = never)
	errBoom error
	onCall  func(call int)
	calls   int
	limits  []int
}

func (f *fakePurger) PurgeExpired(_ context.Context, limit int) (int, error) {
	i := f.calls
	f.calls++
	f.limits = append(f.limits, limit)
	if f.onCall != nil {
		f.onCall(i)
	}
	if f.errAt > 0 && i == f.errAt {
		return 0, f.errBoom
	}
	if i >= len(f.script) {
		i = len(f.script) - 1
	}
	return f.script[i], nil
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestPurgeIdempotencyKeysStopsOnAShortBatch: batches run back to back until
// one comes back short, then the pass ends; every call gets the batch size.
func TestPurgeIdempotencyKeysStopsOnAShortBatch(t *testing.T) {
	f := &fakePurger{script: []int{3, 3, 1, 0}}
	total, err := purgeIdempotencyKeys(context.Background(), f, 3)
	if err != nil || total != 7 || f.calls != 3 {
		t.Fatalf("purge = %d, %v after %d calls, want 7, no error, 3 calls (a short batch ends the pass)", total, err, f.calls)
	}
	for _, limit := range f.limits {
		if limit != 3 {
			t.Fatalf("a batch was called with limit %d, want 3", limit)
		}
	}
}

// TestPurgeIdempotencyKeysStopsOnTheFirstError: a failed batch ends the pass
// with the error; the next tick retries (the purge is idempotent).
func TestPurgeIdempotencyKeysStopsOnTheFirstError(t *testing.T) {
	boom := errors.New("boom")
	f := &fakePurger{script: []int{3}, errAt: 1, errBoom: boom}
	total, err := purgeIdempotencyKeys(context.Background(), f, 3)
	if !errors.Is(err, boom) || total != 3 || f.calls != 2 {
		t.Fatalf("purge = %d, %v after %d calls, want 3, boom, 2 calls", total, err, f.calls)
	}
}

// TestPurgeIdempotencyKeysEndsWithTheContext: a backlog that never shrinks
// still ends when ctx is cancelled, returning what it deleted so far.
func TestPurgeIdempotencyKeysEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakePurger{script: []int{3}, onCall: func(call int) {
		if call == 1 {
			cancel()
		}
	}}
	total, err := purgeIdempotencyKeys(ctx, f, 3)
	if err != nil || total != 6 || f.calls != 2 {
		t.Fatalf("purge = %d, %v after %d calls, want 6, no error, 2 calls", total, err, f.calls)
	}
}

// TestStartIdempotencyPurgeRunsThenStops: the worker purges at once and stops
// (its done channel closes) once stop is called.
func TestStartIdempotencyPurgeRunsThenStops(t *testing.T) {
	f := &fakePurger{script: []int{0}}
	w, stop := startIdempotencyPurge(f, quietLogger())
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for f.calls == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond) // the first pass runs at once, asynchronously
	}
	if f.calls < 1 {
		t.Fatal("the purge worker never ran a pass")
	}
	stop()
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the purge worker did not stop")
	}
}
