package repocontract

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/IQVO/pricing/internal/application/ports"
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
	if _, err := e.Schedules.Current(ctx()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Current before any publish = %v, want ErrNotFound", err)
	}
	s1 := publishSchedule(t, "sch-1", 1, 0)
	fatalIf(t, e.Schedules.Save(ctx(), s1), "save first schedule")
	got, err := e.Schedules.Current(ctx())
	fatalIf(t, err, "current")
	sameSchedule(t, got, s1)

	s2 := publishSchedule(t, "sch-2", 2, 1)
	if err := e.Schedules.Save(ctx(), s2); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a second CURRENT schedule = %v, want ErrConcurrentUpdate", err)
	}
	fatalIf(t, e.UoW.Do(ctx(), func(c context.Context) error {
		if err := s1.Supersede(); err != nil {
			return err
		}
		if err := e.Schedules.Save(c, s1); err != nil {
			return err
		}
		return e.Schedules.Save(c, s2)
	}), "supersede and publish in one unit")
	got, err = e.Schedules.Current(ctx())
	fatalIf(t, err, "current after supersede")
	sameSchedule(t, got, s2)

	dup := publishSchedule(t, "sch-3", 2, 1)
	if err := e.Schedules.Save(ctx(), dup); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a repeated version = %v, want ErrConcurrentUpdate", err)
	}
}
