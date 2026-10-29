package bdd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
)

func registerThen(sc *godog.ScenarioContext, get func() *world) {
	sc.Step(`^the API answers (\d+)$`, func(status int) error { return get().wantStatus(status) })
	sc.Step(`^the API answers (\d+) with problem "([^"]*)"$`, func(status int, slug string) error {
		w := get()
		if err := w.wantStatus(status); err != nil {
			return err
		}
		if ct := w.header["Content-Type"]; len(ct) != 1 || ct[0] != "application/problem+json" {
			return fmt.Errorf("content type = %v", ct)
		}
		return w.wantField("type", "/problems/"+slug)
	})
	sc.Step(`^the response field "([^"]*)" is "([^"]*)"$`, func(path, want string) error { return get().wantField(path, want) })
	sc.Step(`^the response field "([^"]*)" is (\d+)$`, func(path string, want int) error { return get().wantField(path, fmt.Sprint(want)) })
	sc.Step(`^the response is marked replayed$`, func() error {
		if v := get().header["Idempotent-Replayed"]; len(v) != 1 || v[0] != "true" {
			return fmt.Errorf("Idempotent-Replayed = %v", v)
		}
		return nil
	})
	sc.Step(`^I remember the response field "([^"]*)" as "([^"]*)"$`, func(path, name string) error {
		w := get()
		v, err := w.field(path)
		w.saved[name] = v
		return err
	})
	sc.Step(`^(\d+) "([^"]*)" events were published$`, func(n int, name string) error {
		got := 0
		for _, e := range get().store.Events() {
			if e.EventName() == name {
				got++
			}
		}
		if got != n {
			return fmt.Errorf("%d %s events, want %d", got, name, n)
		}
		return nil
	})
}

func (w *world) wantStatus(status int) error {
	if w.status != status {
		return fmt.Errorf("status = %d, want %d: %s", w.status, status, w.body)
	}
	return nil
}

// field reads a dotted path (arrays by index) of the JSON response as text.
func (w *world) field(path string) (string, error) {
	var cur any
	if err := json.Unmarshal([]byte(w.body), &cur); err != nil {
		return "", fmt.Errorf("body is not JSON: %q", w.body)
	}
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[part]
		case []any:
			var i int
			if _, err := fmt.Sscanf(part, "%d", &i); err != nil || i < 0 || i >= len(node) {
				return "", fmt.Errorf("no element %q in %s", part, w.body)
			}
			cur = node[i]
		default:
			return "", fmt.Errorf("no field %q in %s", path, w.body)
		}
	}
	if f, ok := cur.(float64); ok {
		return fmt.Sprintf("%d", int64(f)), nil
	}
	return fmt.Sprint(cur), nil
}

func (w *world) wantField(path, want string) error {
	got, err := w.field(path)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s = %q, want %q (%s)", path, got, want, w.body)
	}
	return nil
}
