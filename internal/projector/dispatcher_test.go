package projector_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/jordisalazar/money-management/internal/projector"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// recordingSubscriber captures every event it receives so the test can
// assert on type + metadata. No external state, lives per-test.
type recordingSubscriber struct {
	got []recordedEvent
}

type recordedEvent struct {
	value any
	meta  eventstore.EventMetadata
}

func (r *recordingSubscriber) Handle(event any, meta eventstore.EventMetadata) {
	r.got = append(r.got, recordedEvent{value: event, meta: meta})
}

type fakeEvent struct {
	WalletID string `json:"wallet_id"`
	Amount   int    `json:"amount"`
}

// buildRecord produces a Kinesis record whose Data field is the
// DynamoDB-stream JSON shape the projector decodes. payload may be nil
// to produce a record with no payload attribute.
func buildRecord(t *testing.T, eventName, eventType, eventID string, payload any) events.KinesisEventRecord {
	t.Helper()
	image := map[string]map[string]string{
		"event_id":   {"S": eventID},
		"event_type": {"S": eventType},
	}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshalling payload: %v", err)
		}
		image["payload"] = map[string]string{"S": string(raw)}
	}
	body := map[string]any{
		"eventName": eventName,
		"dynamodb": map[string]any{
			"NewImage": image,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling record: %v", err)
	}
	return events.KinesisEventRecord{
		EventID: eventID,
		Kinesis: events.KinesisRecord{Data: data},
	}
}

func newSilentDispatcher() (*projector.Dispatcher, *recordingSubscriber) {
	d := projector.NewDispatcher(slog.New(slog.NewTextHandler(silentWriter{}, nil)))
	d.RegisterEvent("FakeEvent", func() any { return &fakeEvent{} })
	rec := &recordingSubscriber{}
	d.Subscribe(rec)
	return d, rec
}

// silentWriter swallows log output so the test binary stays quiet.
type silentWriter struct{}

func (silentWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestDispatcher_DecodesAndFansOut(t *testing.T) {
	d, rec := newSilentDispatcher()
	r := buildRecord(t, "INSERT", "FakeEvent", "evt-1", fakeEvent{WalletID: "w-1", Amount: 100})

	if err := d.HandleBatch(context.Background(), []events.KinesisEventRecord{r}); err != nil {
		t.Fatalf("HandleBatch: %v", err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("subscriber received %d events, want 1", len(rec.got))
	}
	ev, ok := rec.got[0].value.(fakeEvent)
	if !ok {
		t.Fatalf("subscriber got %T, want fakeEvent", rec.got[0].value)
	}
	if ev.WalletID != "w-1" || ev.Amount != 100 {
		t.Errorf("decoded event = %+v, want WalletID=w-1 Amount=100", ev)
	}
	if rec.got[0].meta.EventID != "evt-1" {
		t.Errorf("meta.EventID = %q, want evt-1", rec.got[0].meta.EventID)
	}
}

func TestDispatcher_UnregisteredEventType_Skipped(t *testing.T) {
	d, rec := newSilentDispatcher()
	r := buildRecord(t, "INSERT", "UnknownEvent", "evt-2", fakeEvent{})

	if err := d.HandleBatch(context.Background(), []events.KinesisEventRecord{r}); err != nil {
		t.Fatalf("HandleBatch should swallow unknown event types: %v", err)
	}
	if len(rec.got) != 0 {
		t.Fatalf("subscriber received %d events, want 0 (skipped)", len(rec.got))
	}
}

func TestDispatcher_RemoveEvent_Skipped(t *testing.T) {
	d, rec := newSilentDispatcher()
	r := buildRecord(t, "REMOVE", "FakeEvent", "evt-3", fakeEvent{WalletID: "w-3"})

	if err := d.HandleBatch(context.Background(), []events.KinesisEventRecord{r}); err != nil {
		t.Fatalf("REMOVE should be skipped, got %v", err)
	}
	if len(rec.got) != 0 {
		t.Fatalf("subscriber received %d events on REMOVE, want 0", len(rec.got))
	}
}

func TestDispatcher_MalformedRecord_ReturnsError(t *testing.T) {
	d, rec := newSilentDispatcher()
	r := events.KinesisEventRecord{
		EventID: "evt-bad",
		Kinesis: events.KinesisRecord{Data: []byte("{not valid json")},
	}

	err := d.HandleBatch(context.Background(), []events.KinesisEventRecord{r})
	if err == nil {
		t.Fatal("expected error for malformed record")
	}
	if !strings.Contains(err.Error(), "evt-bad") {
		t.Errorf("error %q should reference the record id", err)
	}
	if len(rec.got) != 0 {
		t.Errorf("subscriber should not have seen anything, got %d", len(rec.got))
	}
}

func TestDispatcher_MissingEventType_ReturnsError(t *testing.T) {
	d, _ := newSilentDispatcher()
	body := map[string]any{
		"eventName": "INSERT",
		"dynamodb":  map[string]any{"NewImage": map[string]any{}},
	}
	data, _ := json.Marshal(body)
	r := events.KinesisEventRecord{EventID: "evt-no-type", Kinesis: events.KinesisRecord{Data: data}}

	err := d.HandleBatch(context.Background(), []events.KinesisEventRecord{r})
	if err == nil || !strings.Contains(err.Error(), "event_type") {
		t.Fatalf("got %v, want missing event_type error", err)
	}
}

func TestDispatcher_FanoutOrder(t *testing.T) {
	d, _ := newSilentDispatcher()
	sub2 := &recordingSubscriber{}
	d.Subscribe(sub2)

	for i := 0; i < 3; i++ {
		r := buildRecord(t, "INSERT", "FakeEvent", fmt.Sprintf("evt-%d", i), fakeEvent{Amount: i})
		if err := d.HandleBatch(context.Background(), []events.KinesisEventRecord{r}); err != nil {
			t.Fatalf("HandleBatch %d: %v", i, err)
		}
	}
	if len(sub2.got) != 3 {
		t.Fatalf("subscriber 2 saw %d events, want 3", len(sub2.got))
	}
	for i, e := range sub2.got {
		if e.value.(fakeEvent).Amount != i {
			t.Errorf("sub2 position %d had Amount=%d, want %d", i, e.value.(fakeEvent).Amount, i)
		}
	}
}
