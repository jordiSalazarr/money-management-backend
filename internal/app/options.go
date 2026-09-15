package app

// StoreMode names a supported event-store backend.
type StoreMode string

const (
	StoreModeMemory    StoreMode = "memory"
	StoreModeKurrentDB StoreMode = "kurrentdb"
)

// Options controls which subsystems BuildEngine wires up.
//
// cmd/server defaults DefaultStore to memory, so it runs with no
// infrastructure at all. Deployed workloads set STORE=kurrentdb.
type Options struct {
	// MountQueryRoutes gates the GET handlers that read from projection
	// stores.
	//
	// When the chosen StoreMode doesn't produce projection stores this
	// flag is silently ignored.
	MountQueryRoutes bool

	// DefaultStore is the fallback when the STORE env var is unset. cmd/server
	// picks memory so it runs with no infrastructure at all; every other entry
	// point picks kurrentdb.
	DefaultStore StoreMode

	// RunAsync starts the projector, the process reactors, the recurring cron
	// and the sweeper in the same process as the HTTP server.
	//
	// Both remaining stores have a global log and need this: without it the
	// read models stay empty and no transfer ever completes.
	RunAsync bool

	// Async tunes the in-process pipeline. The zero value is fine.
	Async AsyncOptions
}
