package eventstore

import (
	"encoding/json"
	"fmt"
)

// Upcaster is one step of a chain: it translates a payload at version From
// into a payload at version From+1. Build one with Typed.
type Upcaster struct {
	eventType string
	from      int
	apply     func(json.RawMessage) (json.RawMessage, error)
}

// Typed builds an upcaster from a plain Go function over the two shapes.
//
// The historical shape stays frozen in a schema/ package and the translation
// is ordinary code, so a renamed or retyped field is a compile error rather
// than a silently-zeroed value at replay time. That matters because the only
// changes that need an upcaster at all are the ones where getting it subtly
// wrong corrupts history: renames, type changes, defaults the zero value
// cannot express, splits and merges, and semantic reinterpretations. Adding or
// removing a field needs nothing — encoding/json already does the right thing.
//
// See ADR 0001.
func Typed[From, To any](eventType string, from int, fn func(From) To) Upcaster {
	return Upcaster{
		eventType: eventType,
		from:      from,
		apply: func(raw json.RawMessage) (json.RawMessage, error) {
			var in From
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("decoding v%d shape: %w", from, err)
			}
			out, err := json.Marshal(fn(in))
			if err != nil {
				return nil, fmt.Errorf("encoding v%d shape: %w", from+1, err)
			}
			return out, nil
		},
	}
}
