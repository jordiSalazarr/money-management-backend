package app

// StoreMode names a supported event-store backend.
type StoreMode string

const (
	StoreModeDynamoDB StoreMode = "dynamodb"
	StoreModeMemory   StoreMode = "memory"
)

// Options controls which subsystems BuildEngine wires up.
//
// The HTTP entry point (cmd/server) defaults DefaultStore to memory and
// gets a self-contained local dev experience. The Lambda entry point
// (cmd/lambda) picks dynamodb.
type Options struct {
	// MountQueryRoutes gates the GET handlers that read from projection
	// stores. True for cmd/server in memory mode (sync in-process
	// projections). For cmd/lambda it's effectively driven by whether
	// AURORA_URL is set — see internal/app/projector.go.
	//
	// When the chosen StoreMode doesn't produce projection stores this
	// flag is silently ignored.
	MountQueryRoutes bool

	// DefaultStore is the fallback when the STORE env var is unset. The
	// HTTP entry point picks memory; the Lambda entry point picks dynamodb.
	DefaultStore StoreMode
}
