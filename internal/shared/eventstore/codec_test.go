package eventstore_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Three generations of one event, as they would appear over time. v1 and v2
// are frozen historical shapes; depositedV3 is what the domain package would
// hold today.
type depositedV1 struct {
	WalletID string `json:"wallet_id"`
	Amount   int    `json:"amount"`
}

type depositedV2 struct {
	WalletID string `json:"wallet_id"`
	Amount   int    `json:"amount"`
	Category string `json:"category"`
}

type depositedV3 struct {
	WalletID string `json:"wallet_id"`
	Amount   int    `json:"amount"`
	Category string `json:"category"`
	Note     string `json:"note"`
}

func (depositedV3) EventType() string { return "MoneyDeposited" }

func recorded(eventType string, version int, payload string) eventstore.Recorded {
	return eventstore.Recorded{
		Event: eventstore.Event{
			EventID:       "e1",
			EventType:     eventType,
			SchemaVersion: version,
			Payload:       json.RawMessage(payload),
			OccurredAt:    time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
		},
		Stream:   eventstore.StreamID{Category: "wallet", ID: "w1"},
		Revision: 0,
	}
}

func TestCodecDecodesAnEventAtTheCurrentVersion(t *testing.T) {
	c := eventstore.NewCodec()
	c.Register("MoneyDeposited", 1, func() any { return &depositedV1{} })

	got, err := c.Decode(recorded("MoneyDeposited", 1, `{"wallet_id":"w1","amount":500}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Decoding yields a value, not a pointer — projections and Apply() switch
	// on value types.
	ev, ok := got.(depositedV1)
	if !ok {
		t.Fatalf("want depositedV1 value, got %T", got)
	}
	if ev.WalletID != "w1" || ev.Amount != 500 {
		t.Errorf("payload not decoded: %+v", ev)
	}
}

func TestCodecRejectsAnEventTypeItDoesNotKnow(t *testing.T) {
	c := eventstore.NewCodec()

	_, err := c.Decode(recorded("SomethingElse", 1, `{}`))
	if !errors.Is(err, eventstore.ErrUnknownEventType) {
		t.Fatalf("want ErrUnknownEventType, got %v", err)
	}
}

func TestCodecRunsTheUpcasterChainInOrder(t *testing.T) {
	c := eventstore.NewCodec()
	c.Register("MoneyDeposited", 3, func() any { return &depositedV3{} })
	c.Upcast(
		eventstore.Typed("MoneyDeposited", 1, func(o depositedV1) depositedV2 {
			return depositedV2{WalletID: o.WalletID, Amount: o.Amount, Category: "uncategorised"}
		}),
		eventstore.Typed("MoneyDeposited", 2, func(o depositedV2) depositedV3 {
			return depositedV3{WalletID: o.WalletID, Amount: o.Amount, Category: o.Category, Note: "backfilled"}
		}),
	)

	got, err := c.Decode(recorded("MoneyDeposited", 1, `{"wallet_id":"w1","amount":500}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	ev := got.(depositedV3)
	if ev.Category != "uncategorised" {
		t.Errorf("1->2 upcaster did not run: category=%q", ev.Category)
	}
	if ev.Note != "backfilled" {
		t.Errorf("2->3 upcaster did not run: note=%q", ev.Note)
	}
	if ev.Amount != 500 || ev.WalletID != "w1" {
		t.Errorf("chain lost data: %+v", ev)
	}
}

func TestCodecTreatsAMissingChainStepAsIdentity(t *testing.T) {
	c := eventstore.NewCodec()
	c.Register("MoneyDeposited", 3, func() any { return &depositedV3{} })
	// Only 2->3 is registered. 1->2 was an additive change that needed no
	// upcaster, but the version was still bumped (ADR 0001), so the chain has
	// a gap it must step over rather than fail on.
	c.Upcast(
		eventstore.Typed("MoneyDeposited", 2, func(o depositedV2) depositedV3 {
			return depositedV3{WalletID: o.WalletID, Amount: o.Amount, Category: o.Category, Note: "n"}
		}),
	)

	got, err := c.Decode(recorded("MoneyDeposited", 1, `{"wallet_id":"w1","amount":500}`))
	if err != nil {
		t.Fatalf("decode across a gap: %v", err)
	}
	ev := got.(depositedV3)
	if ev.Amount != 500 || ev.WalletID != "w1" {
		t.Errorf("identity step lost data: %+v", ev)
	}
	if ev.Category != "" {
		t.Errorf("identity step should leave the absent field zero, got %q", ev.Category)
	}
	if ev.Note != "n" {
		t.Errorf("2->3 upcaster did not run after the gap: %q", ev.Note)
	}
}

func TestCodecDecodesAnEventAlreadyAtTheCurrentVersion(t *testing.T) {
	c := eventstore.NewCodec()
	c.Register("MoneyDeposited", 3, func() any { return &depositedV3{} })
	c.Upcast(eventstore.Typed("MoneyDeposited", 1, func(o depositedV1) depositedV2 {
		t.Fatal("upcaster ran on an event already at the current version")
		return depositedV2{}
	}))

	got, err := c.Decode(recorded("MoneyDeposited", 3, `{"wallet_id":"w1","amount":1,"category":"Salary","note":"x"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.(depositedV3).Category != "Salary" {
		t.Errorf("payload altered: %+v", got)
	}
}

func TestCodecRejectsAnEventNewerThanItUnderstands(t *testing.T) {
	c := eventstore.NewCodec()
	c.Register("MoneyDeposited", 2, func() any { return &depositedV2{} })

	// A rolling deploy can put a newer writer alongside an older reader.
	// Silently treating v3 as v2 would drop whatever v3 added.
	_, err := c.Decode(recorded("MoneyDeposited", 3, `{}`))
	if !errors.Is(err, eventstore.ErrFutureSchemaVersion) {
		t.Fatalf("want ErrFutureSchemaVersion, got %v", err)
	}
}

func TestCodecEncodeStampsTheRegisteredVersion(t *testing.T) {
	c := eventstore.NewCodec()
	c.Register("MoneyDeposited", 3, func() any { return &depositedV3{} })

	env, err := c.Encode(depositedV3{WalletID: "w1", Amount: 42}, "corr-1",
		time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if env.SchemaVersion != 3 {
		t.Errorf("want schema_version 3, got %d", env.SchemaVersion)
	}
	if env.EventType != "MoneyDeposited" {
		t.Errorf("want event type MoneyDeposited, got %s", env.EventType)
	}
	if env.CorrelationID != "corr-1" {
		t.Errorf("want correlation_id corr-1, got %s", env.CorrelationID)
	}
	if env.EventID == "" {
		t.Error("encode must assign an event id")
	}
	var back depositedV3
	if err := json.Unmarshal(env.Payload, &back); err != nil {
		t.Fatalf("payload is not valid json: %v", err)
	}
	if back.Amount != 42 {
		t.Errorf("payload lost data: %+v", back)
	}
}

func TestCodecEncodeRejectsAnUnregisteredType(t *testing.T) {
	c := eventstore.NewCodec()

	_, err := c.Encode(depositedV3{}, "corr-1", time.Now())
	if !errors.Is(err, eventstore.ErrUnknownEventType) {
		t.Fatalf("want ErrUnknownEventType, got %v", err)
	}
}

func TestCodecDecodeAllPreservesOrder(t *testing.T) {
	c := eventstore.NewCodec()
	c.Register("MoneyDeposited", 1, func() any { return &depositedV1{} })

	got, err := c.DecodeAll([]eventstore.Recorded{
		recorded("MoneyDeposited", 1, `{"amount":1}`),
		recorded("MoneyDeposited", 1, `{"amount":2}`),
		recorded("MoneyDeposited", 1, `{"amount":3}`),
	})
	if err != nil {
		t.Fatalf("decode all: %v", err)
	}
	for i, want := range []int{1, 2, 3} {
		if got[i].(depositedV1).Amount != want {
			t.Errorf("position %d: want amount %d, got %d", i, want, got[i].(depositedV1).Amount)
		}
	}
}
