package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const (
	publisherTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	probeType            = "com.retailnetwork.commerce.test.probe.Probed"
)

// tracedHandle runs one message with the given headers through handleTraced and
// returns the span context the handler saw, the ended spans and the outcome.
func tracedHandle(t *testing.T, headers []kafkago.Header, failWith error) (trace.SpanContext, []sdktrace.ReadOnlySpan, error) {
	t.Helper()
	tp, prop := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTracerProvider(tp); otel.SetTextMapPropagator(prop) })
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var seen trace.SpanContext
	c := &Consumer{
		Name: "probe", Topic: "t", Logger: quietLogger(),
		Handlers: map[string]HandlerFunc{probeType: func(ctx context.Context, _ ce.Event) error {
			seen = trace.SpanContextFromContext(ctx)
			return failWith
		}},
	}
	e := ce.New()
	e.SetID("e1")
	e.SetType(probeType)
	e.SetSource("test")
	if err := e.SetData(ce.ApplicationJSON, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	err = c.handleTraced(context.Background(), kafkago.Message{Value: value, Headers: headers})
	return seen, recorder.Ended(), err
}

// The consumer span is a consumer-kind span named "<topic> process" whose parent
// is the publisher's span carried in traceparent, and the handler runs inside it.
func TestConsumerSpanIsParentedToThePublisherTrace(t *testing.T) {
	seen, spans, err := tracedHandle(t, []kafkago.Header{
		{Key: "content-type", Value: []byte("application/cloudevents+json; charset=UTF-8")},
		{Key: "traceparent", Value: []byte(publisherTraceparent)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	span := spans[0]
	if span.SpanKind() != trace.SpanKindConsumer || span.Name() != "t process" {
		t.Errorf("span = %v %q, want consumer \"t process\"", span.SpanKind(), span.Name())
	}
	if got := span.SpanContext().TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id = %s, want the publisher's", got)
	}
	if got := span.Parent().SpanID().String(); got != "00f067aa0ba902b7" {
		t.Errorf("parent span id = %s, want the publish span's", got)
	}
	if seen.SpanID() != span.SpanContext().SpanID() {
		t.Errorf("handler ran under span %s, want the consumer span %s", seen.SpanID(), span.SpanContext().SpanID())
	}
}

func TestConsumerWithoutTraceHeadersStartsANewTrace(t *testing.T) {
	_, spans, err := tracedHandle(t, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	if spans[0].Parent().IsValid() {
		t.Errorf("parent = %v, want none for a message without traceparent", spans[0].Parent())
	}
}

func TestConsumerSpanRecordsAHandlerFailure(t *testing.T) {
	boom := errors.New("boom")
	_, spans, err := tracedHandle(t, []kafkago.Header{{Key: "traceparent", Value: []byte(publisherTraceparent)}}, boom)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the handler's", err)
	}
	if len(spans) != 1 || spans[0].Status().Code != codes.Error {
		t.Fatalf("spans = %v, want an errored consumer span", spans)
	}
}
