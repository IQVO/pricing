package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/IQVO/pricing/internal/application/usecases"
)

// DefaultPolicyInterval is how often the clock-driven policies run.
const DefaultPolicyInterval = 5 * time.Second

// policyRunner is one clock-driven policy pass.
type policyRunner struct {
	name string
	run  func(ctx context.Context) (int, error)
}

// policyRunners are PublishEffectivePriceOnWindowBoundary and the quote expiry.
func policyRunners(d usecases.Deps) []policyRunner {
	return []policyRunner{
		{"publish-boundaries", usecases.PublishBoundaries{Deps: d}.Execute},
		{"expire-quotes", usecases.ExpireQuotes{Deps: d}.Execute},
	}
}

// runPolicies runs every policy once, logging failures; a failing pass is
// retried by the next tick.
func runPolicies(ctx context.Context, logger *slog.Logger, runners []policyRunner) {
	for _, r := range runners {
		n, err := r.run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("policy pass failed", "policy", r.name, "error", err)
			continue
		}
		if n > 0 {
			logger.Info("policy pass done", "policy", r.name, "handled", n)
		}
	}
}

// startPolicies starts the always-on policy loop.
func startPolicies(d usecases.Deps, logger *slog.Logger) (*worker, func()) {
	interval := parsePolicyInterval(os.Getenv("POLICY_INTERVAL"), logger)
	runners := policyRunners(d)
	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "policies", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		logger.Info("policies running", "interval", interval)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			runPolicies(ctx, logger, runners)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return w, stop
}
