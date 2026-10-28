package services

import (
	"log/slog"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
)

// FormatFlow identifies the flow an agent response belongs to for
// formatting-rule matching. Empty fields never satisfy a non-empty rule
// condition, so an unknown dimension simply narrows the set of rules that
// can match.
type FormatFlow struct {
	SourceKind  string // agent_runs.source_kind (alert|cron|slack_mention|manual|proposal)
	TriggerUUID string // agent_runs.source_uuid (alert source instance, listener channel, cron job)
	ChannelUUID string // destination Channel.UUID; "" = unknown or none
	LastSkill   string // agent_runs.last_skill_used
}

// MatchFormattingRule returns the first enabled rule matching the flow, or
// nil when none matches. A rule with a MatchExpression is decided by the
// expression alone (invalid expressions fail safe: the rule is skipped);
// otherwise the non-empty simple match_* conditions must all equal the
// corresponding flow fields. Rules must already be in evaluation order
// (position ASC, id ASC — as returned by database.ListFormattingRules).
func MatchFormattingRule(rules []database.FormattingRule, flow FormatFlow) *database.FormattingRule {
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		if expr := strings.TrimSpace(r.MatchExpression); expr != "" {
			matched, err := EvalMatchExpression(expr, flow)
			if err != nil {
				slog.Warn("formatting rule has an invalid match expression; skipping rule",
					"rule_uuid", r.UUID, "err", err)
				continue
			}
			if matched {
				return r
			}
			continue
		}
		if !conditionMatches(r.MatchSourceKind, flow.SourceKind) {
			continue
		}
		if !conditionMatches(r.MatchSourceUUID, flow.TriggerUUID) {
			continue
		}
		if !conditionMatches(r.MatchChannelUUID, flow.ChannelUUID) {
			continue
		}
		if !conditionMatches(r.MatchLastSkill, flow.LastSkill) {
			continue
		}
		return r
	}
	return nil
}

// conditionMatches reports whether a single rule condition accepts the flow
// value: blank conditions are wildcards, non-blank conditions require an
// exact (trimmed) match.
func conditionMatches(condition, value string) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true
	}
	return condition == strings.TrimSpace(value)
}

// BuildFormatFlow loads the run row and assembles a FormatFlow for rule
// matching. The destination channel UUID is supplied by the caller because
// resolution differs per path (alert routing, listener thread, cron channel).
// Best-effort: a load failure returns a flow carrying only ChannelUUID so
// wildcard/channel rules can still match; it never returns an error.
func BuildFormatFlow(runID, channelUUID string) FormatFlow {
	flow := FormatFlow{ChannelUUID: channelUUID}
	if runID == "" {
		return flow
	}
	run, err := database.GetAgentRun(runID)
	if err != nil {
		slog.Warn("format flow: failed to load run, matching on channel only",
			"run", runID, "err", err)
		return flow
	}
	flow.SourceKind = run.SourceKind
	flow.TriggerUUID = run.SourceUUID
	flow.LastSkill = run.LastSkillUsed
	return flow
}
