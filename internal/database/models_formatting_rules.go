package database

import (
	"time"
)

// FormattingRule is one entry in the ordered per-flow formatting rule list.
// Rules are the only formatting mechanism: the first enabled rule (by
// position ASC, id ASC) whose non-empty match conditions all equal the
// incident's flow identity supplies the format config; when no rule matches
// the response is delivered unformatted.
//
// Empty match_* fields are wildcards; non-empty conditions are ANDed.
type FormattingRule struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	UUID string `gorm:"uniqueIndex;size:36;not null" json:"uuid"`
	Name string `gorm:"size:255;not null" json:"name"`
	// No gorm default tag: a default would silently flip Enabled=false /
	// zero-valued inserts back to the column default. Callers set it
	// explicitly (the API defaults omitted enabled to true).
	Enabled  bool `json:"enabled"`
	Position int  `gorm:"not null;index" json:"position"`

	// Match conditions — empty = wildcard; non-empty conditions are ANDed.
	MatchSourceKind  string `gorm:"size:32" json:"match_source_kind"`  // alert|cron|slack_mention|manual|proposal
	MatchSourceUUID  string `gorm:"size:36" json:"match_source_uuid"`  // trigger: incident.source_uuid (alert source, listener channel, cron job)
	MatchChannelUUID string `gorm:"size:36" json:"match_channel_uuid"` // destination Channel.UUID
	MatchLastSkill   string `gorm:"size:64" json:"match_last_skill"`   // agent_runs.last_skill_used skill name

	// MatchExpression is the advanced alternative to the simple match_*
	// fields: a boolean expression over source_kind/trigger/channel/skill
	// (==, !=, &&, ||, !, parentheses; and/or/not aliases). Either/or per
	// rule — when non-empty the simple fields must be empty and the
	// expression alone decides the match. Parse failures at evaluation time
	// skip the rule (fail-safe).
	MatchExpression string `gorm:"type:text" json:"match_expression"`

	// Format config. Blank SystemPrompt falls back to DefaultFormattingPrompt,
	// blank OutputSchemaExample to the built-in default schema, MaxTokens<=0
	// to 1500 — mirroring the retired global FormattingSettings semantics.
	// No gorm default tags: an explicit temperature of 0 must persist as 0.
	SystemPrompt        string  `gorm:"type:text" json:"system_prompt"`
	OutputSchemaExample string  `gorm:"type:text" json:"output_schema_example"`
	MaxTokens           int     `json:"max_tokens"`
	Temperature         float64 `json:"temperature"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (FormattingRule) TableName() string {
	return "formatting_rules"
}

// ListFormattingRules returns all formatting rules in evaluation order.
func ListFormattingRules() ([]FormattingRule, error) {
	var rules []FormattingRule
	if err := DB.Order("position ASC, id ASC").Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}
