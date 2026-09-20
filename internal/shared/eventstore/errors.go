package eventstore

import "errors"

var (
	ErrConcurrencyConflict = errors.New("concurrency conflict: unexpected version")
	ErrAggregateNotFound   = errors.New("aggregate not found")

	// ErrConnectionClosed means this process's client is finished, not that
	// the store is down. The two look identical from the outside and need
	// opposite responses: an unreachable store is waited out, a closed client
	// never reopens and the process has to be replaced.
	//
	// It is its own sentinel so callers can tell them apart without importing
	// a driver — see registerHealth, which restarts the pod on this and only
	// this.
	ErrConnectionClosed = errors.New("event store connection is closed")
)
