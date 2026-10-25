package repocontract

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/tax"
)

func publishTable(t *testing.T, id string, version, current int64) *tax.TaxTable {
	t.Helper()
	tt, _, err := tax.Publish(tax.ID(id), version, bands(), current, t0)
	fatalIf(t, err, "publish tax table "+id)
	return tt
}

func sameTable(t *testing.T, got, want *tax.TaxTable) {
	t.Helper()
	g, _ := json.Marshal(got.Snapshot())
	w, _ := json.Marshal(want.Snapshot())
	if string(g) != string(w) {
		t.Fatalf("round trip changed the tax table:\n got %s\nwant %s", g, w)
	}
}

func testTaxTables(t *testing.T, e Env) {
	if _, err := e.TaxTables.Current(ctx()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Current before any publish = %v, want ErrNotFound", err)
	}
	t1 := publishTable(t, "tax-1", 1, 0)
	fatalIf(t, e.TaxTables.Save(ctx(), t1), "save first table")
	got, err := e.TaxTables.Current(ctx())
	fatalIf(t, err, "current")
	sameTable(t, got, t1)

	t2 := publishTable(t, "tax-2", 2, 1)
	if err := e.TaxTables.Save(ctx(), t2); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a second CURRENT table = %v, want ErrConcurrentUpdate", err)
	}
	fatalIf(t, e.UoW.Do(ctx(), func(c context.Context) error {
		if err := t1.Supersede(); err != nil {
			return err
		}
		if err := e.TaxTables.Save(c, t1); err != nil {
			return err
		}
		return e.TaxTables.Save(c, t2)
	}), "supersede and publish in one unit")
	got, err = e.TaxTables.Current(ctx())
	fatalIf(t, err, "current after supersede")
	sameTable(t, got, t2)

	dup := publishTable(t, "tax-3", 2, 1)
	if err := e.TaxTables.Save(ctx(), dup); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a repeated version = %v, want ErrConcurrentUpdate", err)
	}
}
