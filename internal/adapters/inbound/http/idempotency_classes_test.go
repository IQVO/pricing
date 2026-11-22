// The repo-specific replay-class checks that need no store: the writer
// discipline of the handlers and the ADR table that must equal the catalogue
// (ADR 0011). The per-class behaviour over a real Postgres lives in the
// integration-tagged replay-class tests; the router-level proof over the
// in-memory double is TestClientErrorsFollowTheirReplayClass.
package http_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	httpadapter "github.com/IQVO/pricing/internal/adapters/inbound/http"
)

// Every refusal a keyed handler writes goes through writeError, the writer that
// marks the capture with the class of its problem; a handler that wrote a
// problem by hand would bypass the class (and be released as unclassified).
func TestHandlersAnswerErrorsOnlyThroughWriteError(t *testing.T) {
	src, err := os.ReadFile("handlers.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"writeProblem(", "writeBody(", "WriteHeader(", "problem+json"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("handlers.go contains %q: answer an error with writeError(w, r, err) so its replay class marks the capture", banned)
		}
	}
}

var adrClassRow = regexp.MustCompile(`^\| ` + "`([a-z-]+)`" + ` \| (FINAL|OUTCOME|RETRYABLE) \|`)

// ADR 0011 carries the class of every problem type with its reason; the table
// and the catalogue must say the same, in both directions.
func TestTheADRDeclaresTheClassOfEveryProblem(t *testing.T) {
	paths, err := filepath.Glob("../../../../docs/adr/0011-*.md")
	if err != nil || len(paths) != 1 {
		t.Fatalf("ADR 0011 not found (%v, %v)", paths, err)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	inADR := map[string]string{}
	for _, line := range regexp.MustCompile(`\r?\n`).Split(string(raw), -1) {
		if m := adrClassRow.FindStringSubmatch(line); m != nil {
			inADR[m[1]] = m[2]
		}
	}
	// Several entries can share a slug (not-found, non-increasing-version): one
	// row per slug, every entry of the slug must agree with it.
	seen := map[string]bool{}
	for _, row := range httpadapter.Catalogue() {
		if got := inADR[row.Slug]; got != row.Class {
			t.Errorf("problem type %q: the ADR says %q, the catalogue %q", row.Slug, got, row.Class)
		}
		seen[row.Slug] = true
	}
	for slug, class := range inADR {
		if !seen[slug] {
			t.Errorf("the ADR classifies %q as %s but the catalogue has no such problem type", slug, class)
		}
	}
}
