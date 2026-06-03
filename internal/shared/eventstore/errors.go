package eventstore

import "errors"

var (
	ErrConcurrencyConflict = errors.New("concurrency conflict: unexpected version")
	ErrAggregateNotFound   = errors.New("aggregate not found")
)
