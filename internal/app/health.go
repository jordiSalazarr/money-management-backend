package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// registerHealth mounts the two probes.
//
// They answer different questions, and conflating them is how a rolling update
// takes an application down: liveness asks "is this process wedged, restart
// it?", readiness asks "should traffic come here yet?". A dependency being
// unreachable is a readiness problem — restarting the pod will not fix
// KurrentDB — so it must never fail liveness.
//
// There is exactly one exception, and it was found the hard way. When
// KurrentDB restarts, the client in this process closes itself permanently:
// every later call returns ErrConnectionClosed, nothing re-dials, and the API
// serves 503 forever while looking perfectly alive. The projector and the
// reactor survive the same event only because they crash and get restarted.
//
// That case inverts the rule above. The fault is not "the dependency is
// down", it is "our client is dead", and restarting the pod is precisely the
// cure. So liveness fails on that one error and on nothing else: a store that
// is merely unreachable reports a different error, keeps liveness passing,
// and is waited out by readiness as before.
func registerHealth(r *gin.Engine, d deps) {
	// Liveness: the process is serving, and its event store client is not in
	// the one state it cannot recover from on its own.
	r.GET("/health/live", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := checkEventStore(ctx, d); errors.Is(err, eventstore.ErrConnectionClosed) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "event store client is closed and cannot reopen; restarting",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Readiness: the backends this instance needs are actually reachable.
	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		checks := gin.H{}
		ready := true

		if err := checkEventStore(ctx, d); err != nil {
			checks["event_store"] = err.Error()
			ready = false
		} else {
			checks["event_store"] = "ok"
		}

		if d.queriesReady {
			if err := checkReadModels(ctx, d); err != nil {
				checks["read_models"] = err.Error()
				ready = false
			} else {
				checks["read_models"] = "ok"
			}
		}

		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"ready": ready, "checks": checks})
	})
}

// checkEventStore reads a stream that will never exist. ErrAggregateNotFound
// is the healthy answer: it means the store was reachable and replied.
func checkEventStore(ctx context.Context, d deps) error {
	_, err := d.store.Load(ctx, eventstore.StreamID{Category: "healthcheck", ID: "probe"})
	if err == nil || err == eventstore.ErrAggregateNotFound {
		return nil
	}
	return err
}

// checkReadModels issues the cheapest real query there is. A GET handler that
// would 500 must not be reported as ready.
func checkReadModels(ctx context.Context, d deps) error {
	_, err := d.queries.processes.ListInFlightBefore(ctx, time.Unix(0, 0).UTC())
	return err
}
