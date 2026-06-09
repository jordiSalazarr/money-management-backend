package app

// StoreMode names a supported event-store backend.
type StoreMode string

const (
	StoreModePostgres StoreMode = "postgres"
	StoreModeDynamoDB StoreMode = "dynamodb"
	StoreModeMemory   StoreMode = "memory"
)

// Options controls which subsystems BuildEngine wires up.
//
// The HTTP entry point (cmd/server) defaults StartBackgroundJobs and
// MountQueryRoutes to true and gets the classic monolithic behaviour.
// The Lambda entry point (cmd/lambda) turns them off — Lambda is
// request-scoped, so long-lived goroutines cannot survive between
// invocations, and the projector / read-models are deferred to Phase 2.
type Options struct {
	// StartBackgroundJobs gates the outbox poller and the
	// recurring-movement processor. Both rely on Postgres and on a
	// long-lived runtime, so they must stay off in Lambda.
	StartBackgroundJobs bool

	// MountQueryRoutes gates the GET handlers that read from projection
	// stores. Off in Lambda until the projector Lambda lands (Phase 2),
	// because there are no read models to serve. Off also means the
	// voice slice is skipped — it depends on projection stores too.
	//
	// When the chosen StoreMode isn't postgres this flag is effectively
	// ignored: no Postgres projection stores get constructed.
	MountQueryRoutes bool

	// DefaultStore is the fallback when the STORE env var is unset. The
	// HTTP entry point picks postgres so local-dev behaviour stays the
	// same; the Lambda entry point picks dynamodb.
	DefaultStore StoreMode
}
