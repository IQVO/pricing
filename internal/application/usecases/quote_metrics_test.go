package usecases_test

import (
	"context"
	"sync"
	"testing"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/usecases"
)

// recordingMetrics counts the ports.QuoteMetrics calls of a use case.
type recordingMetrics struct {
	mu       sync.Mutex
	issued   int
	rejected []string
}

var _ ports.QuoteMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) QuoteIssued(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issued++
}

func (m *recordingMetrics) QuoteRejected(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected = append(m.rejected, reason)
}

func TestIssuePriceQuoteCountsEachAttemptOnceByOutcome(t *testing.T) {
	e := readyForQuotes(t)
	m := &recordingMetrics{}
	e.deps.Metrics = m
	issue := usecases.IssuePriceQuote{Deps: e.deps}

	if _, err := issue.Execute(bg, quoteInput()); err != nil {
		t.Fatal(err)
	}
	if m.issued != 1 || len(m.rejected) != 0 {
		t.Fatalf("after an issue: issued=%d rejected=%v, want 1 / none", m.issued, m.rejected)
	}

	unpriced := quoteInput()
	unpriced.Lines[0].CatalogItemID = "never-priced"
	if _, err := issue.Execute(bg, unpriced); err == nil {
		t.Fatal("a quote of an unpriced item must be refused")
	}
	if m.issued != 1 || len(m.rejected) != 1 || m.rejected[0] != "item-not-priced" {
		t.Fatalf("after a refusal: issued=%d rejected=%v, want 1 / [item-not-priced]", m.issued, m.rejected)
	}
}

func TestIssuePriceQuoteRejectionReasonsAreTheProblemSlugs(t *testing.T) {
	e := newEnv(t)
	e.price("a", 5000)
	e.price("b", 12345)
	m := &recordingMetrics{}
	e.deps.Metrics = m
	issue := usecases.IssuePriceQuote{Deps: e.deps}

	if _, err := issue.Execute(bg, quoteInput()); err == nil {
		t.Fatal("a quote without a charge schedule must be refused")
	}
	e.publishSchedule(1)
	if _, err := issue.Execute(bg, quoteInput()); err == nil {
		t.Fatal("a quote without a tax table must be refused")
	}
	empty := quoteInput()
	empty.Lines, empty.Groups = nil, nil
	if _, err := issue.Execute(bg, empty); err == nil {
		t.Fatal("a quote without lines must be refused")
	}
	want := []string{"no-charge-schedule", "no-tax-table", "empty-quote"}
	if len(m.rejected) != len(want) {
		t.Fatalf("reasons = %v, want %v", m.rejected, want)
	}
	for i := range want {
		if m.rejected[i] != want[i] {
			t.Errorf("reason %d = %q, want %q", i, m.rejected[i], want[i])
		}
	}
}

func TestIssuePriceQuoteWithoutMetricsStillWorks(t *testing.T) {
	e := readyForQuotes(t)
	if _, err := (usecases.IssuePriceQuote{Deps: e.deps}).Execute(bg, quoteInput()); err != nil {
		t.Fatal(err)
	}
}
