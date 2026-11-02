package repocontract

import (
	"encoding/json"
	"testing"

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
	t1 := publishTable(t, "tax-1", 1, 0)
	t2 := publishTable(t, "tax-2", 2, 1)
	dup := publishTable(t, "tax-3", 2, 1)
	testVersionedLifecycle(t, e, e.TaxTables, t1, t2, dup,
		func() error { return t1.Supersede() }, sameTable, "table")
}
