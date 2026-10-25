package http_test

import (
	"fmt"
	"net/http"
	"testing"
)

const tiersBody = `[
 {"tier":"SAME_DAY","charge":%[1]s},{"tier":"NEXT_DAY","charge":%[1]s,"freeOver":%[2]s},
 {"tier":"TWO_DAY","charge":%[1]s},{"tier":"STANDARD","charge":%[1]s}]`

func scheduleBody(version int, tiers string) string {
	return fmt.Sprintf(`{"version":%d,"tiers":%s}`, version, tiers)
}

func TestChargeScheduleRoutes(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.get("/delivery-charge-schedules/current"), http.StatusNotFound, "not-found")
	tiers := fmt.Sprintf(tiersBody, money(500), money(9000))
	pub := e.post("/delivery-charge-schedules", scheduleBody(1, tiers))
	e.wantStatus(pub, http.StatusCreated)
	if v := pub.json(t); v["state"] != "CURRENT" || len(v["tiers"].([]any)) != 4 {
		t.Fatalf("schedule = %s", pub.Body)
	}
	e.wantProblem(e.post("/delivery-charge-schedules", scheduleBody(1, tiers)), http.StatusConflict, "non-increasing-version")
	e.wantProblem(e.post("/delivery-charge-schedules", scheduleBody(2, `[{"tier":"STANDARD","charge":`+money(1)+`}]`)), http.StatusUnprocessableEntity, "incomplete-charge-schedule")
	e.wantStatus(e.post("/delivery-charge-schedules", scheduleBody(2, tiers)), http.StatusCreated)
	cur := e.get("/delivery-charge-schedules/current")
	e.wantStatus(cur, http.StatusOK)
	if cur.json(t)["version"] != float64(2) {
		t.Fatalf("current = %s", cur.Body)
	}
}

func TestTaxTableRoutes(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.get("/tax-tables/current"), http.StatusNotFound, "not-found")
	bands := `[{"postalPrefixFrom":"010","postalPrefixTo":"099","rateBasisPoints":1000}]`
	pub := e.post("/tax-tables", `{"version":1,"bands":`+bands+`}`)
	e.wantStatus(pub, http.StatusCreated)
	if pub.json(t)["state"] != "CURRENT" {
		t.Fatalf("table = %s", pub.Body)
	}
	e.wantProblem(e.post("/tax-tables", `{"version":1,"bands":`+bands+`}`), http.StatusConflict, "non-increasing-version")
	overlap := `[{"postalPrefixFrom":"010","postalPrefixTo":"050","rateBasisPoints":1},{"postalPrefixFrom":"040","postalPrefixTo":"090","rateBasisPoints":1}]`
	e.wantProblem(e.post("/tax-tables", `{"version":2,"bands":`+overlap+`}`), http.StatusUnprocessableEntity, "overlapping-tax-band")
	high := `[{"postalPrefixFrom":"010","postalPrefixTo":"050","rateBasisPoints":10001}]`
	e.wantProblem(e.post("/tax-tables", `{"version":2,"bands":`+high+`}`), http.StatusUnprocessableEntity, "rate-out-of-range")
	e.wantStatus(e.get("/tax-tables/current"), http.StatusOK)
}
