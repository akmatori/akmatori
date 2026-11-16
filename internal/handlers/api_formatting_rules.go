package handlers

import (
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	formattingMaxTokensMin     = 1
	formattingMaxTokensMax     = 8000
	formattingTemperatureMin   = 0.0
	formattingTemperatureMax   = 2.0
	formattingSystemPromptMax  = 8 * 1024
	formattingSchemaExampleMax = 8 * 1024
	formattingRuleNameMax      = 255
	formattingRuleSkillMax     = 64
	formattingRuleExprMax      = 4 * 1024
)

var validFormattingSourceKinds = map[string]bool{
	database.IncidentSourceKindAlert:         true,
	database.IncidentSourceKindCron:          true,
	database.IncidentSourceKindSlackMention:  true,
	database.IncidentSourceKindSlackListener: true,
	database.IncidentSourceKindManual:        true,
	database.IncidentSourceKindProposal:      true,
}

// handleFormattingRules handles GET (ordered list) and POST (create) on
// /api/formatting-rules.
func (h *APIHandler) handleFormattingRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rules, err := database.ListFormattingRules()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to list formatting rules")
			return
		}
		api.RespondJSON(w, http.StatusOK, rules)

	case http.MethodPost:
		var req api.CreateFormattingRuleRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		rule := database.FormattingRule{
			UUID:                uuid.New().String(),
			Name:                strings.TrimSpace(req.Name),
			Enabled:             true,
			MatchSourceKind:     strings.TrimSpace(req.MatchSourceKind),
			MatchSourceUUID:     strings.TrimSpace(req.MatchSourceUUID),
			MatchChannelUUID:    strings.TrimSpace(req.MatchChannelUUID),
			MatchLastSkill:      strings.TrimSpace(req.MatchLastSkill),
			MatchExpression:     strings.TrimSpace(req.MatchExpression),
			SystemPrompt:        req.SystemPrompt,
			OutputSchemaExample: req.OutputSchemaExample,
			MaxTokens:           1500,
			Temperature:         0.2,
		}
		if req.Enabled != nil {
			rule.Enabled = *req.Enabled
		}
		if req.MaxTokens != nil {
			rule.MaxTokens = *req.MaxTokens
		}
		if req.Temperature != nil {
			rule.Temperature = *req.Temperature
		}
		if msg := validateFormattingRule(&rule); msg != "" {
			api.RespondError(w, http.StatusBadRequest, msg)
			return
		}

		if err := database.DB.Transaction(func(tx *gorm.DB) error {
			var maxPos *int
			if err := tx.Model(&database.FormattingRule{}).
				Select("MAX(position)").Scan(&maxPos).Error; err != nil {
				return err
			}
			if maxPos != nil {
				rule.Position = *maxPos + 1
			}
			return tx.Create(&rule).Error
		}); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to create formatting rule")
			return
		}
		api.RespondJSON(w, http.StatusCreated, rule)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleFormattingRuleByUUID handles PUT (partial update) and DELETE on
// /api/formatting-rules/{uuid}.
func (h *APIHandler) handleFormattingRuleByUUID(w http.ResponseWriter, r *http.Request) {
	ruleUUID := r.PathValue("uuid")

	var rule database.FormattingRule
	if err := database.DB.Where("uuid = ?", ruleUUID).First(&rule).Error; err != nil {
		api.RespondError(w, http.StatusNotFound, "Formatting rule not found")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req api.UpdateFormattingRuleRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.Name != nil {
			rule.Name = strings.TrimSpace(*req.Name)
		}
		if req.Enabled != nil {
			rule.Enabled = *req.Enabled
		}
		if req.MatchSourceKind != nil {
			rule.MatchSourceKind = strings.TrimSpace(*req.MatchSourceKind)
		}
		if req.MatchSourceUUID != nil {
			rule.MatchSourceUUID = strings.TrimSpace(*req.MatchSourceUUID)
		}
		if req.MatchChannelUUID != nil {
			rule.MatchChannelUUID = strings.TrimSpace(*req.MatchChannelUUID)
		}
		if req.MatchLastSkill != nil {
			rule.MatchLastSkill = strings.TrimSpace(*req.MatchLastSkill)
		}
		if req.MatchExpression != nil {
			rule.MatchExpression = strings.TrimSpace(*req.MatchExpression)
		}
		if req.SystemPrompt != nil {
			rule.SystemPrompt = *req.SystemPrompt
		}
		if req.OutputSchemaExample != nil {
			rule.OutputSchemaExample = *req.OutputSchemaExample
		}
		if req.MaxTokens != nil {
			rule.MaxTokens = *req.MaxTokens
		}
		if req.Temperature != nil {
			rule.Temperature = *req.Temperature
		}
		if msg := validateFormattingRule(&rule); msg != "" {
			api.RespondError(w, http.StatusBadRequest, msg)
			return
		}

		if err := database.DB.Save(&rule).Error; err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to update formatting rule")
			return
		}
		api.RespondJSON(w, http.StatusOK, rule)

	case http.MethodDelete:
		if err := database.DB.Delete(&rule).Error; err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to delete formatting rule")
			return
		}
		api.RespondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleFormattingRulesReorder handles PUT /api/formatting-rules/reorder.
// The body must list every existing rule UUID exactly once; positions are
// reassigned to the list order in one transaction.
func (h *APIHandler) handleFormattingRulesReorder(w http.ResponseWriter, r *http.Request) {
	var req api.ReorderFormattingRulesRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	err := database.DB.Transaction(func(tx *gorm.DB) error {
		var existing []database.FormattingRule
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}
		if len(existing) != len(req.UUIDs) {
			return errReorderSetMismatch
		}
		known := make(map[string]bool, len(existing))
		for _, rule := range existing {
			known[rule.UUID] = true
		}
		seen := make(map[string]bool, len(req.UUIDs))
		for _, id := range req.UUIDs {
			if !known[id] || seen[id] {
				return errReorderSetMismatch
			}
			seen[id] = true
		}
		for idx, id := range req.UUIDs {
			if err := tx.Model(&database.FormattingRule{}).
				Where("uuid = ?", id).
				Update("position", idx).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if err == errReorderSetMismatch {
			api.RespondError(w, http.StatusBadRequest, "uuids must contain every existing rule UUID exactly once")
			return
		}
		api.RespondError(w, http.StatusInternalServerError, "Failed to reorder formatting rules")
		return
	}

	rules, err := database.ListFormattingRules()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to list formatting rules")
		return
	}
	api.RespondJSON(w, http.StatusOK, rules)
}

var errReorderSetMismatch = &reorderSetMismatchError{}

type reorderSetMismatchError struct{}

func (*reorderSetMismatchError) Error() string { return "reorder set mismatch" }

// validateFormattingRule enforces field constraints shared by create and
// update. Returns a user-facing message, or "" when the rule is valid.
func validateFormattingRule(rule *database.FormattingRule) string {
	if rule.Name == "" {
		return "name is required"
	}
	if len(rule.Name) > formattingRuleNameMax {
		return "name must be 255 bytes or fewer"
	}
	if rule.MatchSourceKind != "" && !validFormattingSourceKinds[rule.MatchSourceKind] {
		return "match_source_kind must be one of: alert, cron, slack_mention, manual, proposal"
	}
	if rule.MatchSourceUUID != "" {
		if _, err := uuid.Parse(rule.MatchSourceUUID); err != nil {
			return "match_source_uuid must be a valid UUID"
		}
	}
	if rule.MatchChannelUUID != "" {
		if _, err := uuid.Parse(rule.MatchChannelUUID); err != nil {
			return "match_channel_uuid must be a valid UUID"
		}
	}
	if len(rule.MatchLastSkill) > formattingRuleSkillMax {
		return "match_last_skill must be 64 bytes or fewer"
	}
	if rule.MatchExpression != "" {
		if rule.MatchSourceKind != "" || rule.MatchSourceUUID != "" || rule.MatchChannelUUID != "" || rule.MatchLastSkill != "" {
			return "a rule uses either match_expression or the simple match fields, not both — clear one side"
		}
		if len(rule.MatchExpression) > formattingRuleExprMax {
			return "match_expression must be 4096 bytes or fewer"
		}
		if err := services.ValidateMatchExpression(rule.MatchExpression); err != nil {
			return "match_expression: " + err.Error()
		}
	}
	if len(rule.SystemPrompt) > formattingSystemPromptMax {
		return "system_prompt must be 8192 bytes or fewer"
	}
	if len(rule.OutputSchemaExample) > formattingSchemaExampleMax {
		return "output_schema_example must be 8192 bytes or fewer"
	}
	if err := services.ValidateSchemaExample(rule.OutputSchemaExample); err != nil {
		return "output_schema_example: " + err.Error()
	}
	if rule.MaxTokens < formattingMaxTokensMin || rule.MaxTokens > formattingMaxTokensMax {
		return "max_tokens must be between 1 and 8000"
	}
	if rule.Temperature < formattingTemperatureMin || rule.Temperature > formattingTemperatureMax {
		return "temperature must be between 0 and 2"
	}
	return ""
}
