package architecture

// MCP server read-only fitness (ADR 0009). The static authority sensor
// (mcp_authority_fitness_test.go) pins the declarations and the dependency
// rule (fitness_test.go) pins the direction of the imports; these sensors pin
// what the MCP server can REACH:
//
//   - TestMCPServerIsReadOnly: the only use cases the adapter and cmd/mcp name
//     are the two queries behind REST GETs, and Deps holds exactly them, so no
//     command use case (and with it no outbox write) is reachable.
//   - TestMCPBinaryHasNoKafkaOrRelay: cmd/mcp imports no Kafka, outbox-relay
//     or REST-server package and reads no Kafka/outbox/consumer variable.
//   - TestMCPSensorsBite proves each detector flags what it must and passes
//     what it must not.

import (
	"go/parser"
	"strings"
	"testing"
)

const mcpCmdDir = "../../cmd/mcp"

// mcpAllowedUseCases are the two queries the MCP server may call (the use
// cases of GET /item-prices/{catalogItemId} and GET /price-quotes/{quoteId}).
var mcpAllowedUseCases = map[string]bool{"GetItemPrice": true, "GetPriceQuote": true, "Deps": true}

// forbiddenForMCPBinary names why an import of cmd/mcp is not allowed, or "".
// The request logger is shared with the REST router and is allowed here (the
// fleet middleware order), unlike in cmd/a2a.
func forbiddenForMCPBinary(path string) string {
	for _, bad := range []string{"adapters/outbound/kafka", "adapters/outbound/outbox", "adapters/inbound/kafka", "adapters/inbound/http", "adapters/outbound/memory"} {
		if strings.Contains(path, "/internal/"+bad) {
			return bad
		}
	}
	if strings.Contains(path, "segmentio/kafka-go") {
		return "kafka-go"
	}
	return ""
}

func TestMCPServerIsReadOnly(t *testing.T) {
	var files []string
	for _, dir := range []string{mcpAdapterDir, mcpCmdDir} {
		files = append(files, goFilesUnder(t, dir, false)...)
	}
	if len(files) < 2 {
		t.Fatal("MCP sources not found: the sensor would pass vacuously")
	}
	for _, path := range files {
		for _, name := range usecaseSelectors(parseGo(t, path, nil, 0)) {
			if !mcpAllowedUseCases[name] {
				t.Errorf("%s names usecases.%s: the MCP server is read-only and may only use GetItemPrice and GetPriceQuote (ADR 0009)", path, name)
			}
		}
	}
	if got := strings.Join(structFields(parseGo(t, mcpAdapterDir+"/tools.go", nil, 0), "Deps"), ","); got != "GetItemPrice,GetPriceQuote" {
		t.Errorf("mcp.Deps fields = %s, want exactly GetItemPrice,GetPriceQuote (queries only)", got)
	}
}

func TestMCPBinaryHasNoKafkaOrRelay(t *testing.T) {
	files := goFilesUnder(t, mcpCmdDir, false)
	if len(files) == 0 {
		t.Fatal("no non-test Go file under cmd/mcp: the sensor would pass vacuously")
	}
	for _, path := range files {
		f := parseGo(t, path, nil, 0)
		for _, p := range importPaths(f) {
			if why := forbiddenForMCPBinary(p); why != "" {
				t.Errorf("%s imports %s (%s): cmd/mcp is read-only: no Kafka, no outbox relay, no REST server", path, p, why)
			}
		}
		for _, lit := range stringLiterals(f) {
			if why := forbiddenA2AEnv(lit); why != "" {
				t.Errorf("%s has the string %q (%s): cmd/mcp reads and renders no Kafka/outbox/consumer variable", path, lit, why)
			}
		}
	}
}

func TestMCPSensorsBite(t *testing.T) {
	const m = modulePath
	for imp, want := range map[string]string{
		m + "/internal/adapters/outbound/kafka":     "adapters/outbound/kafka",
		m + "/internal/adapters/outbound/outbox":    "adapters/outbound/outbox",
		m + "/internal/adapters/inbound/kafka":      "adapters/inbound/kafka",
		m + "/internal/adapters/inbound/http":       "adapters/inbound/http",
		"github.com/segmentio/kafka-go":             "kafka-go",
		m + "/internal/adapters/outbound/postgres":  "",
		m + "/internal/adapters/outbound/clock":     "",
		m + "/internal/adapters/outbound/telemetry": "",
		m + "/internal/adapters/inbound/requestlog": "",
	} {
		if got := forbiddenForMCPBinary(imp); got != want {
			t.Fatalf("forbiddenForMCPBinary(%s) = %q, want %q", imp, got, want)
		}
	}
	use := parseGo(t, "u.go", "package mcp\nvar _ = usecases.IssuePriceQuote{}\nvar _ usecases.GetItemPrice\nvar _ usecases.ListItemPrices\n", parser.SkipObjectResolution)
	var flagged []string
	for _, name := range usecaseSelectors(use) {
		if !mcpAllowedUseCases[name] {
			flagged = append(flagged, name)
		}
	}
	if strings.Join(flagged, ",") != "IssuePriceQuote,ListItemPrices" {
		t.Fatalf("the allowlist flagged %v, want the command and the bootstrap list", flagged)
	}
}
