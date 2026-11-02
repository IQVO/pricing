package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/pricing/internal/application/ports"
)

// versionedRepo is the slice of a versioned-aggregate repository the shared
// lifecycle below exercises: read the CURRENT aggregate, save a new version.
type versionedRepo[T any] interface {
	Current(ctx context.Context) (*T, error)
	Save(ctx context.Context, agg *T) error
}

// testVersionedLifecycle pins the invariants every versioned-current
// repository must hold, in order: Current before any publish is ErrNotFound;
// the first publish becomes CURRENT; a competing CURRENT is rejected as
// ErrConcurrentUpdate; supersede + publish in ONE unit of work swaps CURRENT
// atomically; and repeating a published version is rejected as
// ErrConcurrentUpdate. first is the first version, second supersedes it, and
// repeat re-publishes second's version; supersede retires first so second can
// become CURRENT in the unit of work; same compares a round-tripped aggregate
// against the expected one.
func testVersionedLifecycle[T any](t *testing.T, e Env, repo versionedRepo[T], first, second, repeat *T, supersede func() error, same func(*testing.T, *T, *T), what string) {
	t.Helper()
	if _, err := repo.Current(ctx()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Current before any publish = %v, want ErrNotFound", err)
	}
	fatalIf(t, repo.Save(ctx(), first), "save first "+what)
	got, err := repo.Current(ctx())
	fatalIf(t, err, "current")
	same(t, got, first)

	if err := repo.Save(ctx(), second); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a second CURRENT %s = %v, want ErrConcurrentUpdate", what, err)
	}
	fatalIf(t, e.UoW.Do(ctx(), func(c context.Context) error {
		if err := supersede(); err != nil {
			return err
		}
		if err := repo.Save(c, first); err != nil {
			return err
		}
		return repo.Save(c, second)
	}), "supersede and publish in one unit")
	got, err = repo.Current(ctx())
	fatalIf(t, err, "current after supersede")
	same(t, got, second)

	if err := repo.Save(ctx(), repeat); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a repeated version = %v, want ErrConcurrentUpdate", err)
	}
}
