package repocontract

import (
	"encoding/json"
	"testing"

	"github.com/IQVO/pricing/internal/domain/schedule"
)

func publishSchedule(t *testing.T, id string, version, current int64) *schedule.DeliveryChargeSchedule {
	t.Helper()
	s, _, err := schedule.Publish(schedule.ID(id), version, fullTiers(), current, cur, t0)
	fatalIf(t, err, "publish schedule "+id)
	return s
}

func sameSchedule(t *testing.T, got, want *schedule.DeliveryChargeSchedule) {
	t.Helper()
	g, _ := json.Marshal(got.Snapshot())
	w, _ := json.Marshal(want.Snapshot())
	if string(g) != string(w) {
		t.Fatalf("round trip changed the schedule:\n got %s\nwant %s", g, w)
	}
}

func testSchedules(t *testing.T, e Env) {
	s1 := publishSchedule(t, "sch-1", 1, 0)
	s2 := publishSchedule(t, "sch-2", 2, 1)
	dup := publishSchedule(t, "sch-3", 2, 1)
	testVersionedLifecycle(t, e, e.Schedules, s1, s2, dup,
		func() error { return s1.Supersede() }, sameSchedule, "schedule")
}
