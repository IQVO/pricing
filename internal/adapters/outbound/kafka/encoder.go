package kafka

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/IQVO/pricing/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/pricing/internal/application/outbox"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// Encoder implements outbox.Encoder: each domain event becomes one outbox
// message holding the CloudEvents bytes, the Kafka key (the subject) and the
// content-type header. The CloudEvents `id` is minted HERE, once, and persisted
// with the outbox row, so a relay retry republishes the same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
	// Prefix, when set, is prepended to both topics; tests use it to isolate
	// their topics on a shared broker.
	Prefix string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ outbox.Encoder = (*Encoder)(nil)

// Encode encodes events in order. An event type it does not publish is a
// programming error and fails the whole call.
func (e *Encoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		data, stream, err := payloadFor(ev)
		if err != nil {
			return nil, err
		}
		msg, err := buildMessage(ev, newID(), e.Prefix+topicFor(stream), stream, data)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func topicFor(stream string) string {
	if stream == cloudevents.StreamAnalytics {
		return TopicAnalytics
	}
	return TopicEvents
}

// buildMessage wraps data in the CloudEvents envelope. The dataschema URN's
// stream segment names the stream the event is published on (the fleet event
// standard): `events` on the integration topic, `analytics` on the analytics
// topic. The model's domain-events page pins urn:retailnetwork:pricing:<stream>:<Event>:v1.
func buildMessage(ev shared.Event, id, topic, stream string, data any) (outbox.Message, error) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(), Time: ev.OccurredAt(),
		Stream: stream, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID: id, Topic: topic, EventType: cloudevents.Type(ev.Entity(), ev.EventName()), Subject: ev.SubjectID(),
		Key: []byte(ev.SubjectID()), DataSchema: cloudevents.DataSchema(stream, ev.EventName(), schemaVersion),
		Value: value, Headers: []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}
