package reactor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/reactor"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type spyHandler struct {
	seen []any
	meta []eventstore.EventMetadata
	err  error
}

func (s *spyHandler) Handle(_ context.Context, event any, meta eventstore.EventMetadata) error {
	s.seen = append(s.seen, event)
	s.meta = append(s.meta, meta)
	return s.err
}

func recordedDeposit(t *testing.T, codec *eventstore.Codec) eventstore.Recorded {
	t.Helper()
	env, err := codec.Encode(wallet.MoneyDeposited{
		WalletID: "w1", UserID: "u1", Amount: 500, Currency: "EUR",
		Reason: "salary", Category: "Salary",
	}, "corr-1", time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return eventstore.Recorded{
		Event:  env,
		Stream: eventstore.StreamID{Category: "wallet", ID: "w1"},
	}
}

// Every process is offered every event and ignores what is not its own, so
// adding a process is one more argument at the wiring site and nothing else.
func TestItOffersTheEventToEveryProcess(t *testing.T) {
	codec := eventcatalog.NewCodec()
	a, b := &spyHandler{}, &spyHandler{}

	if err := reactor.NewRouter(codec, nil, a, b).Handle(context.Background(), recordedDeposit(t, codec)); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(a.seen) != 1 || len(b.seen) != 1 {
		t.Fatalf("processes saw %d and %d events, want 1 each", len(a.seen), len(b.seen))
	}
	if _, ok := a.seen[0].(wallet.MoneyDeposited); !ok {
		t.Errorf("got %T, want a decoded wallet.MoneyDeposited value", a.seen[0])
	}
}

// A process error propagates. That is what makes a transient fault safe: the
// subscription stops without advancing, so redelivery resumes exactly where it
// left off. Swallowing it here is how the old router lost work.
func TestAProcessErrorPropagates(t *testing.T) {
	codec := eventcatalog.NewCodec()
	boom := errors.New("kurrentdb unreachable")

	err := reactor.NewRouter(codec, nil, &spyHandler{err: boom}).
		Handle(context.Background(), recordedDeposit(t, codec))

	if !errors.Is(err, boom) {
		t.Fatalf("want the process error to propagate, got %v", err)
	}
}

func TestAnUnknownEventTypeIsAnError(t *testing.T) {
	spy := &spyHandler{}
	err := reactor.NewRouter(eventcatalog.NewCodec(), nil, spy).Handle(context.Background(),
		eventstore.Recorded{
			Event:  eventstore.Event{EventType: "SomethingNobodyRegistered", SchemaVersion: 1, Payload: []byte(`{}`)},
			Stream: eventstore.StreamID{Category: "wallet", ID: "w1"},
		})

	if !errors.Is(err, eventstore.ErrUnknownEventType) {
		t.Fatalf("want ErrUnknownEventType, got %v", err)
	}
	if len(spy.seen) != 0 {
		t.Error("nothing should have been dispatched")
	}
}

// The correlation id is what a reactor stamps onto everything it writes, so a
// single user action stays traceable across every stream a process touches.
func TestItPassesTheCorrelationIDOn(t *testing.T) {
	codec := eventcatalog.NewCodec()
	spy := &spyHandler{}

	if err := reactor.NewRouter(codec, nil, spy).Handle(context.Background(), recordedDeposit(t, codec)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := spy.meta[0].CorrelationID; got != "corr-1" {
		t.Errorf("CorrelationID = %q, want corr-1", got)
	}
}
