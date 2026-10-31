package architecture

// A2A inbound adapter fitness (ADR 0007). pricing has no MCP surface, so the
// agent is READ-ONLY by construction and these sensors pin that construction:
//
//   - TestA2AAdapterDependsOnlyInward: internal/adapters/inbound/a2a imports
//     only the application layer and the domain: never an outbound adapter,
//     a sibling inbound adapter or cmd.
//   - TestNothingDependsOnTheA2AAdapter: only cmd/a2a imports it.
//   - TestNoOutboundA2AClient: no shipped (non-test) code imports the SDK's
//     a2aclient: this context receives delegation, it does not delegate.
//   - TestA2AAgentIsReadOnly: the only use cases the adapter and cmd/a2a name
//     are the two queries behind REST GETs; Deps holds nothing else, so no
//     command use case (and with it no outbox write) is reachable.
//   - TestA2ABinaryHasNoKafkaOrRelay: cmd/a2a imports no Kafka, outbox-relay
//     or REST-server package and reads no Kafka/outbox/consumer variable.
//   - TestA2ASensorsBite proves each detector flags what it must and passes
//     what it must not.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	a2aAdapterDir = "../adapters/inbound/a2a"
	a2aCmdDir     = "../../cmd/a2a"
	modulePrefix  = modulePath + "/internal/"
)

// a2aAllowedUseCases are the two queries the agent may call (the use cases of
// GET /item-prices/{catalogItemId} and GET /price-quotes/{quoteId}).
var a2aAllowedUseCases = map[string]bool{"GetItemPrice": true, "GetPriceQuote": true, "Deps": true}

// parseGo parses src (a file name for messages, or a path read from disk when
// src is nil).
func parseGo(t *testing.T, filename string, src any, mode parser.Mode) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, mode)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	return f
}

// importPaths returns the unquoted import paths of f.
func importPaths(f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// outwardImports returns the imports of src that a pure inward adapter must
// not have: any module-internal package outside application/** and domain/**,
// and anything under cmd.
func outwardImports(f *ast.File) []string {
	var bad []string
	for _, p := range importPaths(f) {
		switch {
		case strings.HasPrefix(p, modulePath+"/cmd"):
			bad = append(bad, p)
		case strings.HasPrefix(p, modulePrefix):
			rest := strings.TrimPrefix(p, modulePrefix)
			if !strings.HasPrefix(rest, "application/") && !strings.HasPrefix(rest, "domain/") {
				bad = append(bad, p)
			}
		}
	}
	return bad
}

// usecaseSelectors returns the Sel names of every `usecases.X` in f.
func usecaseSelectors(f *ast.File) []string {
	var names []string
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "usecases" {
				names = append(names, sel.Sel.Name)
			}
		}
		return true
	})
	return names
}

// stringLiterals returns every string literal of f, unquoted.
func stringLiterals(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// forbiddenForA2ABinary names why an import of cmd/a2a is not allowed, or "".
func forbiddenForA2ABinary(path string) string {
	for _, bad := range []string{"adapters/outbound/kafka", "adapters/outbound/outbox", "adapters/inbound/kafka", "adapters/inbound/http", "adapters/inbound/requestlog", "adapters/outbound/memory"} {
		if strings.Contains(path, "/internal/"+bad) {
			return bad
		}
	}
	if strings.Contains(path, "segmentio/kafka-go") {
		return "kafka-go"
	}
	return ""
}

// forbiddenA2AEnv names why a string literal of cmd/a2a is not allowed (it
// would read or render a Kafka/outbox/consumer variable), or "".
func forbiddenA2AEnv(lit string) string {
	for _, bad := range []string{"KAFKA", "OUTBOX", "CONSUMER_GROUP", "EVENT_PUBLISHER", "POLICY_INTERVAL"} {
		if strings.Contains(strings.ToUpper(lit), bad) {
			return bad
		}
	}
	return ""
}

func TestA2AAdapterDependsOnlyInward(t *testing.T) {
	files := goFilesUnder(t, a2aAdapterDir, false)
	if len(files) == 0 {
		t.Fatal("no non-test Go file under internal/adapters/inbound/a2a: the sensor would pass vacuously")
	}
	for _, path := range files {
		for _, bad := range outwardImports(parseGo(t, path, nil, parser.ImportsOnly)) {
			t.Errorf("%s imports %s: the A2A adapter depends only on internal/application/** and internal/domain/** (never an outbound adapter, a sibling inbound adapter or cmd)", path, bad)
		}
	}
}

func TestNothingDependsOnTheA2AAdapter(t *testing.T) {
	const adapter = modulePath + "/internal/adapters/inbound/a2a"
	for _, root := range []string{"../../internal", "../../cmd"} {
		for _, path := range goFilesUnder(t, root, true) {
			if strings.Contains(path, "internal/adapters/inbound/a2a/") || strings.Contains(path, "cmd/a2a/") {
				continue
			}
			for _, p := range importPaths(parseGo(t, path, nil, parser.ImportsOnly)) {
				if p == adapter {
					t.Errorf("%s imports the A2A adapter: only cmd/a2a (the composition root) may", path)
				}
			}
		}
	}
}

func TestNoOutboundA2AClient(t *testing.T) {
	const client = "github.com/a2aproject/a2a-go/v2/a2aclient"
	for _, root := range []string{"../../internal", "../../cmd"} {
		for _, path := range goFilesUnder(t, root, false) {
			for _, p := range importPaths(parseGo(t, path, nil, parser.ImportsOnly)) {
				if p == client || strings.HasPrefix(p, client+"/") {
					t.Errorf("%s imports %s: pricing receives delegation, it must not add an outbound A2A client", path, p)
				}
			}
		}
	}
}

func TestA2AAgentIsReadOnly(t *testing.T) {
	var files []string
	for _, dir := range []string{a2aAdapterDir, a2aCmdDir} {
		files = append(files, goFilesUnder(t, dir, false)...)
	}
	if len(files) < 2 {
		t.Fatal("A2A sources not found: the sensor would pass vacuously")
	}
	for _, path := range files {
		for _, name := range usecaseSelectors(parseGo(t, path, nil, 0)) {
			if !a2aAllowedUseCases[name] {
				t.Errorf("%s names usecases.%s: the A2A agent is read-only and may only use %v", path, name, allowedList())
			}
		}
	}
	// Deps holds exactly the two queries.
	if got := strings.Join(structFields(parseGo(t, a2aAdapterDir+"/server.go", nil, 0), "Deps"), ","); got != "GetItemPrice,GetPriceQuote" {
		t.Errorf("a2a.Deps fields = %s, want exactly GetItemPrice,GetPriceQuote (queries only)", got)
	}
}

// structFields returns the sorted field names of the struct type named name.
func structFields(f *ast.File, name string) []string {
	var fields []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != name {
			return true
		}
		if st, ok := ts.Type.(*ast.StructType); ok {
			for _, fld := range st.Fields.List {
				for _, id := range fld.Names {
					fields = append(fields, id.Name)
				}
			}
		}
		return true
	})
	sort.Strings(fields)
	return fields
}

func allowedList() []string {
	var out []string
	for k := range a2aAllowedUseCases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestA2ABinaryHasNoKafkaOrRelay(t *testing.T) {
	files := goFilesUnder(t, a2aCmdDir, false)
	if len(files) == 0 {
		t.Fatal("no non-test Go file under cmd/a2a: the sensor would pass vacuously")
	}
	for _, path := range files {
		f := parseGo(t, path, nil, 0)
		for _, p := range importPaths(f) {
			if why := forbiddenForA2ABinary(p); why != "" {
				t.Errorf("%s imports %s (%s): cmd/a2a is read-only: no Kafka, no outbox relay, no REST server", path, p, why)
			}
		}
		for _, lit := range stringLiterals(f) {
			if why := forbiddenA2AEnv(lit); why != "" {
				t.Errorf("%s has the string %q (%s): cmd/a2a reads and renders no Kafka/outbox/consumer variable", path, lit, why)
			}
		}
	}
}

func TestA2ASensorsBite(t *testing.T) {
	const m = modulePath
	src := func(imports ...string) *ast.File {
		var b strings.Builder
		b.WriteString("package a2a\nimport (\n")
		for _, i := range imports {
			fmt.Fprintf(&b, "%q\n", i)
		}
		b.WriteString(")\n")
		return parseGo(t, "src.go", b.String(), parser.ImportsOnly)
	}
	for name, tc := range map[string]struct {
		imports []string
		want    int
	}{
		"application and domain pass":    {[]string{m + "/internal/application/usecases", m + "/internal/application/ports", m + "/internal/domain/shared"}, 0},
		"third party passes":             {[]string{"github.com/a2aproject/a2a-go/v2/a2a", "log/slog"}, 0},
		"an outbound adapter is flagged": {[]string{m + "/internal/adapters/outbound/postgres"}, 1},
		"a sibling inbound is flagged":   {[]string{m + "/internal/adapters/inbound/http"}, 1},
		"cmd is flagged":                 {[]string{m + "/cmd/api"}, 1},
		"a look-alike path passes":       {[]string{"example.com/internal/adapters/outbound/postgres"}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got := outwardImports(src(tc.imports...)); len(got) != tc.want {
				t.Fatalf("findings = %v, want %d", got, tc.want)
			}
		})
	}

	use := parseGo(t, "u.go", "package a2a\nvar _ = usecases.CreateItemPrice{}\nvar _ usecases.GetItemPrice\nvar _ usecases.SetListPrice\n", 0)
	if got := usecaseSelectors(use); len(got) != 3 || got[0] != "CreateItemPrice" {
		t.Fatalf("usecase selectors = %v", got)
	}
	if !a2aAllowedUseCases["GetItemPrice"] || a2aAllowedUseCases["CreateItemPrice"] || a2aAllowedUseCases["IssuePriceQuote"] || a2aAllowedUseCases["ListItemPrices"] {
		t.Fatal("the read-only allowlist must hold exactly the two queries (and Deps)")
	}

	for imp, want := range map[string]string{
		m + "/internal/adapters/outbound/kafka":     "adapters/outbound/kafka",
		m + "/internal/adapters/outbound/outbox":    "adapters/outbound/outbox",
		m + "/internal/adapters/inbound/kafka":      "adapters/inbound/kafka",
		m + "/internal/adapters/inbound/http":       "adapters/inbound/http",
		"github.com/segmentio/kafka-go":             "kafka-go",
		m + "/internal/adapters/outbound/postgres":  "",
		m + "/internal/adapters/outbound/clock":     "",
		m + "/internal/adapters/outbound/telemetry": "",
	} {
		if got := forbiddenForA2ABinary(imp); got != want {
			t.Fatalf("forbiddenForA2ABinary(%s) = %q, want %q", imp, got, want)
		}
	}
	for lit, want := range map[string]string{
		"KAFKA_BROKERS": "KAFKA", "OUTBOX_RELAY_INTERVAL": "OUTBOX", "OFFER_AVAILABILITY_CONSUMER_GROUP": "CONSUMER_GROUP",
		"EVENT_PUBLISHER": "EVENT_PUBLISHER", "POLICY_INTERVAL": "POLICY_INTERVAL", "DATABASE_URL": "", "A2A_AGENT_URL": "",
	} {
		if got := forbiddenA2AEnv(lit); got != want {
			t.Fatalf("forbiddenA2AEnv(%q) = %q, want %q", lit, got, want)
		}
	}
}
