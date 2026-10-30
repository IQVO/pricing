package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/IQVO/pricing/internal/application/ports"
)

// tracerName is the OTel instrumentation scope for A2A skill spans.
const tracerName = "github.com/IQVO/pricing/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card).
const (
	skillItemPrice   = "item_price"
	skillQuoteStatus = "quote_status"
)

// maxIDLength bounds an identifier argument, like the REST adapter's path
// parameters.
const maxIDLength = 128

// nameable reports whether id can name a resource at all. The REST adapter
// answers 404 for an identifier longer than maxIDLength and for one carrying
// U+0000 (PostgreSQL text cannot hold it: the statement would abort with
// SQLSTATE 22021 and surface as a 500), so this adapter answers the same 404
// before the identifier reaches a use case.
func nameable(id string) bool {
	return len(id) <= maxIDLength && !strings.ContainsRune(id, 0)
}

// skillRequest is the wire contract of every skill call: the first meaningful
// message part carries (as a Data part, or a Text part holding the same JSON)
// the skill id and its arguments. Strict on purpose: an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored. (Go's
// encoding/json matches field names case-insensitively, so "catalogitemid"
// still reaches catalogItemId; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// itemPriceArgs and quoteStatusArgs are the per-skill argument shapes.
type itemPriceArgs struct {
	CatalogItemID string `json:"catalogItemId"`
}

type quoteStatusArgs struct {
	QuoteID string `json:"quoteId"`
}

// result is what a skill produces on success: its DTO and the one-line summary.
type result struct {
	data    skillData
	summary string
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR 0007).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, prob := parseSkillRequest(ec)
	if prob != nil {
		return reply(ec, envelope{Problem: prob}, prob.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	res, prob := run(ctx, deps, req)
	if prob != nil {
		span.SetStatus(codes.Error, prob.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: prob}, prob.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: &res.data}, res.summary)
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (result, *problem) {
	switch req.Skill {
	case skillItemPrice:
		return deps.itemPrice(ctx, req.Arguments)
	case skillQuoteStatus:
		return deps.quoteStatus(ctx, req.Arguments)
	default:
		return result{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first
// meaningful part. A Data part (or a Text part holding the same JSON object)
// is accepted; anything else is a request-shape problem, answered, not
// raised, because a malformed delegation is a business answer for the
// calling agent, not a transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// itemPrice is the item_price read skill: GetItemPrice, the query behind
// GET /item-prices/{catalogItemId}.
func (d Deps) itemPrice(ctx context.Context, rawArgs json.RawMessage) (result, *problem) {
	var args itemPriceArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return result{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	if strings.TrimSpace(args.CatalogItemID) == "" {
		return result{}, problemInvalidArgument(errors.New("catalogItemId is required"))
	}
	notFound := "no item price exists for that catalogItemId"
	if !nameable(args.CatalogItemID) {
		return result{}, problemNotFound(notFound)
	}
	v, err := d.GetItemPrice.Execute(ctx, args.CatalogItemID)
	if err != nil {
		return result{}, problemFromUseCase(ctx, err, notFound)
	}
	dto := itemPriceOut(v)
	return result{data: skillData{ItemPrice: &dto}, summary: dto.summary()}, nil
}

// quoteStatus is the quote_status read skill: GetPriceQuote, the query behind
// GET /price-quotes/{quoteId}.
func (d Deps) quoteStatus(ctx context.Context, rawArgs json.RawMessage) (result, *problem) {
	var args quoteStatusArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return result{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	if strings.TrimSpace(args.QuoteID) == "" {
		return result{}, problemInvalidArgument(errors.New("quoteId is required"))
	}
	notFound := "no price quote exists for that quoteId"
	if !nameable(args.QuoteID) {
		return result{}, problemNotFound(notFound)
	}
	v, err := d.GetPriceQuote.Execute(ctx, args.QuoteID)
	if err != nil {
		return result{}, problemFromUseCase(ctx, err, notFound)
	}
	dto := priceQuoteOut(v)
	return result{data: skillData{PriceQuote: &dto}, summary: dto.summary()}, nil
}

func problemNotFound(detail string) *problem {
	return &problem{Type: problemTypeBase + "/not-found", Title: "Not found", Detail: detail, Status: 404}
}

// problemFromUseCase maps a use case error to a problem answer, using the
// SAME classification as the REST adapter (internal/adapters/inbound/http
// problemFor) for the two reads this agent offers: ports.ErrNotFound is 404,
// anything else is a 500 whose detail never leaks the internal message (it is
// logged instead, like REST). Domain rejections are expected business
// outcomes and are answered, never raised as JSON-RPC errors, so the calling
// agent always receives a structured, machine-readable result.
func problemFromUseCase(ctx context.Context, err error, notFoundDetail string) *problem {
	if errors.Is(err, ports.ErrNotFound) {
		return problemNotFound(notFoundDetail)
	}
	slog.ErrorContext(ctx, "a2a skill failed", "error", err)
	return &problem{Type: problemTypeBase + "/internal-error", Title: "Internal server error", Detail: "an unexpected error occurred", Status: 500}
}
