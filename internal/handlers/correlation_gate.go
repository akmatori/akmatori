package handlers

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// correlationGateMaxWait bounds how long an alert waits for the gate. One
// holder needs at most one correlation call (45 s) plus a few DB writes, so
// this is about two full calls. In a storm the later alerts stop waiting and
// correlate without the gate (the behaviour before the gate existed); the
// post-investigation merge pass still joins any duplicate they open.
var correlationGateMaxWait = 90 * time.Second

// correlationGateLogWait is the wait after which a gate entry is logged, so a
// queue that forms in a storm shows up in the API log.
const correlationGateLogWait = time.Second

// correlationGate lets one alert at a time go from correlation to a stored
// alert row. Without it, two alerts that arrive together are correlated in
// parallel: neither sees the other's incident, because it does not exist
// yet, and both open one (gcore 2026-10-30: two RX-drops alerts on one host,
// 3 s apart, two investigations, merged 8 min later).
type correlationGate struct {
	once    sync.Once
	slot    chan struct{}
	waiting atomic.Int32
}

// enter waits for the gate and returns the function that releases it. The
// release is idempotent, so a caller can release early on its main path and
// still defer it for the error paths. After correlationGateMaxWait the alert
// goes on without the gate and the release is a no-op.
func (g *correlationGate) enter(alertName string) (release func()) {
	g.once.Do(func() { g.slot = make(chan struct{}, 1) })

	queued := g.waiting.Add(1)
	start := time.Now()
	timer := time.NewTimer(correlationGateMaxWait)
	defer timer.Stop()

	select {
	case g.slot <- struct{}{}:
		g.waiting.Add(-1)
		if waited := time.Since(start); waited >= correlationGateLogWait {
			slog.Info("correlation gate entered after wait", "alert_name", alertName,
				"waited_ms", waited.Milliseconds(), "queue", queued)
		}
		var once sync.Once
		return func() { once.Do(func() { <-g.slot }) }
	case <-timer.C:
		g.waiting.Add(-1)
		slog.Warn("correlation gate busy, correlating without it", "alert_name", alertName,
			"waited_ms", correlationGateMaxWait.Milliseconds(), "queue", queued)
		return func() {}
	}
}
