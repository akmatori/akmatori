package mcp

import "context"

type runIDKey struct{}

// RunIDHeader carries the agent run id (agent_runs.run_id). X-Incident-ID is
// the run's scope, which follow-up turns reuse; the run id names the one
// run. Tools that write (memory.write, memory.delete) require it.
const RunIDHeader = "X-Run-ID"

// WithRunID returns ctx carrying the run id.
func WithRunID(ctx context.Context, runID string) context.Context {
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, runID)
}

// RunIDFromContext returns the run id of the call, or "".
func RunIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(runIDKey{}).(string)
	return v
}
