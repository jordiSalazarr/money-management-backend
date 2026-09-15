package projector

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Lag is how far one projection is behind the end of the log.
//
// It is the only honest thing a projector's liveness probe can report. "The
// process is running" says nothing: a projector can be up, healthy, and
// twenty minutes behind, serving a read model that quietly disagrees with the
// event log.
type Lag struct {
	Projection string
	Checkpoint eventstore.Position
	Head       eventstore.Position
	// Behind is head minus checkpoint, in log positions. Not events — the
	// positions are byte offsets into the log, so the number is a magnitude
	// rather than a count. Its shape over time is what matters: flat is
	// caught up, climbing is falling behind.
	Behind uint64
	// CaughtUp is true when the projection has read everything that existed
	// when it was measured.
	CaughtUp bool
}

// LagReporter measures every projection against the head of the log.
type LagReporter struct {
	checkpoints CheckpointStore
	head        eventstore.Headed
	names       []string

	mu     sync.RWMutex
	last   []Lag
	lastAt time.Time
}

func NewLagReporter(checkpoints CheckpointStore, head eventstore.Headed, names []string) *LagReporter {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return &LagReporter{checkpoints: checkpoints, head: head, names: sorted}
}

// Measure reads the head once and every checkpoint against it.
//
// Reading the head first and comparing everything to that one value keeps the
// projections comparable: measuring each against its own freshly-read head
// would make a busy log look like lag that is not there.
func (r *LagReporter) Measure(ctx context.Context) ([]Lag, error) {
	head, err := r.head.Head(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading log head: %w", err)
	}

	out := make([]Lag, 0, len(r.names))
	for _, name := range r.names {
		at, err := r.checkpoints.Load(ctx, name)
		if err != nil {
			return nil, err
		}

		var behind uint64
		if head.Commit > at.Commit {
			behind = head.Commit - at.Commit
		}
		out = append(out, Lag{
			Projection: name,
			Checkpoint: at,
			Head:       head,
			Behind:     behind,
			CaughtUp:   behind == 0,
		})
	}

	r.mu.Lock()
	r.last, r.lastAt = out, time.Now()
	r.mu.Unlock()
	return out, nil
}

// Handler serves the metrics endpoint and a liveness probe that means
// something.
//
// /metrics is Prometheus text format. /health/live reports unhealthy only when
// the head itself cannot be read — being behind is not a reason to restart a
// projector, it is a reason to leave it alone and let it catch up. Restarting
// one that is behind makes it further behind.
func (r *LagReporter) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		lags, err := r.Measure(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}

		var b strings.Builder
		b.WriteString("# HELP projection_lag_positions How far a projection is behind the head of the log.\n")
		b.WriteString("# TYPE projection_lag_positions gauge\n")
		for _, l := range lags {
			fmt.Fprintf(&b, "projection_lag_positions{projection=%q} %d\n", l.Projection, l.Behind)
		}
		b.WriteString("# HELP projection_checkpoint_position The position a projection has committed up to.\n")
		b.WriteString("# TYPE projection_checkpoint_position gauge\n")
		for _, l := range lags {
			fmt.Fprintf(&b, "projection_checkpoint_position{projection=%q} %d\n", l.Projection, l.Checkpoint.Commit)
		}
		b.WriteString("# HELP projection_log_head_position The position of the last event in the log.\n")
		b.WriteString("# TYPE projection_log_head_position gauge\n")
		if len(lags) > 0 {
			fmt.Fprintf(&b, "projection_log_head_position %d\n", lags[0].Head.Commit)
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(b.String()))
	})

	mux.HandleFunc("/health/live", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()

		if _, err := r.head.Head(ctx); err != nil {
			http.Error(w, "cannot reach the event store: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	return mux
}
