package projector_test

import (
	"errors"
	"testing"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/projector"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type recordingSubscriber struct {
	seen []any
	meta []eventstore.EventMetadata
}

func (r *recordingSubscriber) Handle(event any, meta eventstore.EventMetadata) {
	r.seen = append(r.seen, event)
	r.meta = append(r.meta, meta)
}

func recordedDeposit(t *testing.T, codec *eventstore.Codec) eventstore.Recorded {
	t.Helper()
	env, err := codec.Encode(wallet.MoneyDeposited{
		WalletID: "w1", UserID: "u1", Amount: 500, Currency: "EUR",
		Reason: "salary", Category: "Salary",
	}, "corr-1", sampleTime())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return eventstore.Recorded{
		Event:    env,
		Stream:   eventstore.StreamID{Category: "wallet", ID: "w1"},
		Revision: 3,
		Position: eventstore.Position{Commit: 9, Prepare: 9},
	}
}

func TestItFansOutToEverySubscriber(t *testing.T) {
	codec := eventcatalog.NewCodec()
	a, b := &recordingSubscriber{}, &recordingSubscriber{}
	d := projector.NewDispatcher(codec, nil)
	d.Subscribe(a)
	d.Subscribe(b)

	if err := d.Handle(recordedDeposit(t, codec)); err != nil {
		t.Fatalf("handle: %v", err)
	}

	for name, sub := range map[string]*recordingSubscriber{"a": a, "b": b} {
		if len(sub.seen) != 1 {
			t.Fatalf("subscriber %s saw %d events, want 1", name, len(sub.seen))
		}
		if _, ok := sub.seen[0].(wallet.MoneyDeposited); !ok {
			t.Errorf("subscriber %s got %T, want a decoded wallet.MoneyDeposited value", name, sub.seen[0])
		}
	}
}

// The metadata a projection needs for a deterministic row id must survive the
// hop, or every projection silently starts inventing ids.
func TestItPassesTheMetadataProjectionsKeyOff(t *testing.T) {
	codec := eventcatalog.NewCodec()
	sub := &recordingSubscriber{}
	d := projector.NewDispatcher(codec, nil)
	d.Subscribe(sub)

	rec := recordedDeposit(t, codec)
	if err := d.Handle(rec); err != nil {
		t.Fatalf("handle: %v", err)
	}

	got := sub.meta[0]
	if got.EventID != rec.EventID {
		t.Errorf("EventID = %q, want %q", got.EventID, rec.EventID)
	}
	if got.Stream != rec.Stream || got.Revision != rec.Revision || got.Position != rec.Position {
		t.Errorf("provenance lost: %+v", got)
	}
	if got.CorrelationID != "corr-1" {
		t.Errorf("CorrelationID = %q, want corr-1", got.CorrelationID)
	}
}

// An event type the codec has never heard of means the registry and the log
// disagree. Carrying on would silently skip history, so it must be an error —
// not a logged warning, which is how the old pipeline lost events.
func TestAnUnknownEventTypeIsAnError(t *testing.T) {
	sub := &recordingSubscriber{}
	d := projector.NewDispatcher(eventcatalog.NewCodec(), nil)
	d.Subscribe(sub)

	err := d.Handle(eventstore.Recorded{
		Event:  eventstore.Event{EventType: "SomethingNobodyRegistered", SchemaVersion: 1, Payload: []byte(`{}`)},
		Stream: eventstore.StreamID{Category: "wallet", ID: "w1"},
	})
	if !errors.Is(err, eventstore.ErrUnknownEventType) {
		t.Fatalf("want ErrUnknownEventType, got %v", err)
	}
	if len(sub.seen) != 0 {
		t.Error("nothing should have been fanned out")
	}
}

func TestADispatcherWithNoSubscribersIsFine(t *testing.T) {
	codec := eventcatalog.NewCodec()
	d := projector.NewDispatcher(codec, nil)
	if err := d.Handle(recordedDeposit(t, codec)); err != nil {
		t.Fatalf("handle: %v", err)
	}
}
