package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/pricing/internal/adapters/outbound/memory"
	"github.com/IQVO/pricing/internal/application/usecases"
)

func TestRunPoliciesLogsAFailureAndKeepsGoing(t *testing.T) {
	var buf bytes.Buffer
	ran := 0
	runners := []policyRunner{
		{"broken", func(context.Context) (int, error) { return 0, errors.New("db down") }},
		{"busy", func(context.Context) (int, error) { ran++; return 3, nil }},
	}
	runPolicies(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)), runners)
	if ran != 1 || !strings.Contains(buf.String(), "db down") || !strings.Contains(buf.String(), "handled=3") {
		t.Fatalf("ran = %d log = %s", ran, buf.String())
	}
}

func TestStartPoliciesStopsWhenClosed(t *testing.T) {
	t.Setenv("POLICY_INTERVAL", "10ms")
	w, stop := startPolicies(nilDeps(), quiet())
	time.Sleep(30 * time.Millisecond)
	stop()
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the policy loop did not stop")
	}
}

// nilDeps are dependencies over the in-memory doubles (test only): the loop
// finds nothing due.
func nilDeps() usecases.Deps {
	c := memory.NewClock(time.Now())
	st := memory.NewStore(c)
	return usecases.Deps{ItemPrices: st.ItemPrices(), Quotes: st.Quotes(), UoW: st.UnitOfWork(), Clock: c}
}
