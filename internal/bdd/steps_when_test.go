package bdd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cucumber/godog"

	"github.com/IQVO/pricing/internal/application/usecases"
)

func registerWhen(sc *godog.ScenarioContext, get func() *world) {
	sc.Step(`^the API gets "([^"]*)"$`, func(path string) error { return get().send("GET", path, "", "") })
	sc.Step(`^the API posts to "([^"]*)" with Idempotency-Key "([^"]*)" the body:$`, func(path, key string, body *godog.DocString) error {
		return get().send("POST", path, key, body.Content)
	})
	sc.Step(`^the API posts to "([^"]*)" the body:$`, func(path string, body *godog.DocString) error {
		w := get()
		w.seq++
		return w.send("POST", path, fmt.Sprintf("auto-%d", w.seq), body.Content)
	})
	sc.Step(`^the API posts to "([^"]*)" with no body$`, func(path string) error {
		w := get()
		w.seq++
		return w.send("POST", path, fmt.Sprintf("auto-%d", w.seq), "")
	})
	sc.Step(`^the API posts to "([^"]*)" with an Idempotency-Key of (\d+) characters the body:$`, func(path string, n int, body *godog.DocString) error {
		return get().send("POST", path, strings.Repeat("k", n), body.Content)
	})
	sc.Step(`^the API puts to "([^"]*)" the body:$`, func(path string, body *godog.DocString) error {
		return get().send("PUT", path, "", body.Content)
	})
	sc.Step(`^the API posts to "([^"]*)" without an Idempotency-Key$`, func(path string) error {
		return get().send("POST", path, "", "{}")
	})
	sc.Step(`^offer-availability discontinues the item "([^"]*)"$`, func(id string) error {
		w := get()
		w.seq++
		return (usecases.Consumer{Deps: w.deps}).CatalogItemDiscontinued(context.Background(), fmt.Sprintf("ev-%d", w.seq), "discontinued", id)
	})
	sc.Step(`^the policies run$`, func() error {
		w := get()
		if _, err := (usecases.PublishBoundaries{Deps: w.deps}).Execute(context.Background()); err != nil {
			return err
		}
		_, err := (usecases.ExpireQuotes{Deps: w.deps}).Execute(context.Background())
		return err
	})
}

// send performs one request; key "" sends no Idempotency-Key.
func (w *world) send(method, path, key, body string) error {
	req, err := http.NewRequest(method, w.srv.URL+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	w.status, w.header, w.body = resp.StatusCode, resp.Header, string(b)
	return nil
}

// expect sends a request with a fresh key and requires the status.
func (w *world) expect(status int, method, path, body string) error {
	w.seq++
	if err := w.send(method, path, fmt.Sprintf("setup-%d", w.seq), body); err != nil {
		return err
	}
	if w.status != status {
		return fmt.Errorf("%s %s = %d, want %d: %s", method, path, w.status, status, w.body)
	}
	return nil
}
