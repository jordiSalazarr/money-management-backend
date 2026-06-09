// Package kinesisdecode decodes the JSON wire format that DynamoDB's
// Kinesis Data Streams export emits.
//
// Each Kinesis record's Data field carries a JSON document mirroring the
// DDB Streams record shape — an envelope around `dynamodb.NewImage`,
// which itself is a map of attribute name → typed-attribute object.
//
// Only the attribute types the events table actually uses (S and N) are
// modelled. Adding M / L / B can wait until a domain event needs them.
package kinesisdecode

import (
	"encoding/json"
	"errors"
)

// Record is the slice of the DynamoDB-stream JSON envelope we read.
// Only EventName and Dynamodb.NewImage are needed by current callers;
// the rest is dropped on the floor.
type Record struct {
	EventName string `json:"eventName"`
	Dynamodb  struct {
		NewImage Image `json:"NewImage"`
	} `json:"dynamodb"`
}

// Image is the attribute map. Each value is a JSON object whose single
// key is the attribute's type tag (S, N).
type Image map[string]Attr

// Attr models the typed-attribute wrapper. Pointers let us distinguish
// "absent" from "present and zero".
type Attr struct {
	S *string `json:"S,omitempty"`
	N *string `json:"N,omitempty"`
}

// Unmarshal parses the Kinesis record Data bytes into a Record.
func Unmarshal(data []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, err
	}
	return r, nil
}

// ErrAttributeMissing is returned by Image.String when the requested
// attribute name isn't present in the image at all.
var ErrAttributeMissing = errors.New("attribute missing")

// String returns the string form of a String or Number attribute. Both
// arrive as strings on the wire; callers that need an int parse from the
// returned value.
func (m Image) String(name string) (string, bool) {
	v, ok := m[name]
	if !ok {
		return "", false
	}
	switch {
	case v.S != nil:
		return *v.S, true
	case v.N != nil:
		return *v.N, true
	default:
		return "", false
	}
}
