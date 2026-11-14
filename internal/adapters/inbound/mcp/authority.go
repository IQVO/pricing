package mcp

// Authority (Agentic Standard v2, AS-1). Every registered tool declares
// exactly one level in its Meta under authorityMetaKey; the fleet rule is in
// warehouse-docs agents/fleet/agent-authority.md. This context's MCP surface
// is read-only (ADR 0009), so every tool is L1: a pure read of this context's
// own state that changes nothing. There is no L2 (nothing recommends), no L3
// (nothing writes) and no L4 (no ADR of this context grants an auto-act
// envelope), so no constant for them is declared. The tools declare the level
// as an inline `mcp.Meta{authorityMetaKey: authorityL1}` literal, which the
// fleet's static sensor (internal/architecture/mcp_authority_fitness_test.go)
// reads as the wire contract.
const (
	// authorityMetaKey is the tools/list _meta key carrying a tool's level.
	authorityMetaKey = "com.warehouse/authority"
	// authorityL1 reads facts and changes nothing.
	authorityL1 = "L1"
)
