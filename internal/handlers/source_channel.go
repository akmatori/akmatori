package handlers

import "fmt"

// withSourceChannel prefixes an agent task with the channel the work arrived
// from, e.g. "Source channel: #csqo_downtime".
//
// Skills and runbooks routinely branch on the originating channel — the
// freemember skill, for example, drills straight into per-product agencies for
// alerts from the per-product downtime channel instead of sweeping all four
// projects. Before this line existed the agent had no way to see the channel,
// so those rules could never fire and every alert fell through to the generic
// workflow.
//
// An empty label yields the task unchanged: an unresolvable channel should
// leave the prompt alone rather than print a raw ID the agent cannot match a
// rule against.
func withSourceChannel(label, task string) string {
	if label == "" {
		return task
	}
	return fmt.Sprintf("Source channel: %s\n\n%s", label, task)
}
