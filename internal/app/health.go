package app

import (
	"context"
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
func registerHealth(r *gin.Engine, d deps) {
	// Liveness: the process is running and serving. Deliberately checks
	// nothing else.
	r.GET("/health/live", func(c *gin.Context) {
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
