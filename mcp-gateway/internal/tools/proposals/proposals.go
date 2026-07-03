package proposals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// Proposal kinds — mirror of the main API's constants
// (internal/database/models_proposals.go).
const (
	kindRunbookNew        = "runbook_new"
	kindRunbookUpdate     = "runbook_update"
	kindMemoryNew         = "memory_new"
	kindMemoryUpdate      = "memory_update"
	kindCronNew           = "cron_new"
	kindCronUpdate        = "cron_update"
	kindSkillPromptUpdate = "skill_prompt_update"
)

const statusPending = "pending"

var validKinds = map[string]bool{
	kindRunbookNew:        true,
	kindRunbookUpdate:     true,
	kindMemoryNew:         true,
	kindMemoryUpdate:      true,
	kindCronNew:           true,
	kindCronUpdate:        true,
	kindSkillPromptUpdate: true,
}

var validMemoryTypes = map[string]bool{
	"host":             true,
	"incident_pattern": true,
	"tool_quirk":       true,
	"feedback":         true,
}

// ProposalsTool lets agents create, inspect, and revise self-improvement
// proposals. It queries the gateway's own DB connection directly, like the
// incidents tool. All writes are constrained: Create inserts pending rows
// with server-side snapshots; UpdateDraft only touches pending rows.
type ProposalsTool struct {
	db     *gorm.DB
	logger *log.Logger
}

// NewProposalsTool creates a new ProposalsTool.
func NewProposalsTool(db *gorm.DB, logger *log.Logger) *ProposalsTool {
	return &ProposalsTool{db: db, logger: logger}
}

// isUpdateKind reports whether the kind targets an existing entity.
func isUpdateKind(kind string) bool {
	switch kind {
	case kindRunbookUpdate, kindMemoryUpdate, kindCronUpdate, kindSkillPromptUpdate:
		return true
	}
	return false
}

// requireString extracts a non-empty string field from a content object.
func requireString(m map[string]interface{}, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("proposed_content.%s is required", key)
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("proposed_content.%s must be a non-empty string", key)
	}
	return s, nil
}

// validateContent enforces the per-kind proposed_content JSON shape.
func validateContent(kind string, m map[string]interface{}) error {
	switch kind {
	case kindRunbookNew, kindRunbookUpdate:
		for _, k := range []string{"title", "content"} {
			if _, err := requireString(m, k); err != nil {
				return err
			}
		}
	case kindMemoryNew, kindMemoryUpdate:
		for _, k := range []string{"scope", "name", "body"} {
			if _, err := requireString(m, k); err != nil {
				return err
			}
		}
		memType, err := requireString(m, "type")
		if err != nil {
			return err
		}
		if !validMemoryTypes[memType] {
			return fmt.Errorf("proposed_content.type must be one of host, incident_pattern, tool_quirk, feedback")
		}
	case kindCronNew, kindCronUpdate:
		for _, k := range []string{"name", "prompt"} {
			if _, err := requireString(m, k); err != nil {
				return err
			}
		}
		schedule, err := requireString(m, "schedule")
		if err != nil {
			return err
		}
		// Cheap syntactic check only; the cron runner does full robfig
		// validation at apply time.
		if len(strings.Fields(schedule)) != 5 {
			return errors.New("proposed_content.schedule must be a 5-field cron expression (m h dom mon dow)")
		}
		if v, ok := m["tool_logical_names"]; ok {
			arr, ok := v.([]interface{})
			if !ok {
				return errors.New("proposed_content.tool_logical_names must be an array of strings")
			}
			for _, e := range arr {
				if _, ok := e.(string); !ok {
					return errors.New("proposed_content.tool_logical_names must be an array of strings")
				}
			}
		}
	case kindSkillPromptUpdate:
		for _, k := range []string{"skill_name", "prompt"} {
			if _, err := requireString(m, k); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown proposal kind %q", kind)
	}
	return nil
}

// snapshotForTarget resolves the live target of an *_update proposal and
// returns its JSON snapshot in the same shape as proposed_content. For
// skill_prompt_update the prompt lives only on disk in the API container, so
// the snapshot is left empty ("") and the API backfills it lazily; this
// function still validates that the skill exists and is not a system skill.
func (t *ProposalsTool) snapshotForTarget(ctx context.Context, kind, targetRef string) (string, error) {
	switch kind {
	case kindRunbookUpdate:
		id, err := strconv.ParseUint(targetRef, 10, 64)
		if err != nil {
			return "", fmt.Errorf("target_ref for runbook_update must be a runbook ID: %q", targetRef)
		}
		var rb database.Runbook
		if err := t.db.WithContext(ctx).First(&rb, uint(id)).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", fmt.Errorf("runbook %s not found", targetRef)
			}
			return "", err
		}
		return marshalSnapshot(map[string]interface{}{"title": rb.Title, "content": rb.Content})
	case kindMemoryUpdate:
		scope, name, ok := strings.Cut(targetRef, "/")
		if !ok || scope == "" || name == "" {
			return "", fmt.Errorf("target_ref for memory_update must be \"<scope>/<name>\": %q", targetRef)
		}
		var mem database.Memory
		if err := t.db.WithContext(ctx).Where("scope = ? AND name = ?", scope, name).First(&mem).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", fmt.Errorf("memory %s not found", targetRef)
			}
			return "", err
		}
		return marshalSnapshot(map[string]interface{}{
			"scope": mem.Scope, "type": mem.Type, "name": mem.Name,
			"description": mem.Description, "body": mem.Body,
		})
	case kindCronUpdate:
		var job database.CronJob
		if err := t.db.WithContext(ctx).Preload("Tools").Where("uuid = ?", targetRef).First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", fmt.Errorf("cron job %s not found", targetRef)
			}
			return "", err
		}
		return marshalSnapshot(map[string]interface{}{
			"name": job.Name, "schedule": job.Schedule, "prompt": job.Prompt,
			"tool_logical_names": toolLogicalNames(job.Tools),
		})
	case kindSkillPromptUpdate:
		var skill database.Skill
		if err := t.db.WithContext(ctx).Where("name = ?", targetRef).First(&skill).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", fmt.Errorf("skill %q not found", targetRef)
			}
			return "", err
		}
		if skill.IsSystem {
			return "", fmt.Errorf("skill %q is a system skill; its prompt is hardcoded and cannot be changed by proposal", targetRef)
		}
		// Prompt lives on disk in the API container — API backfills lazily.
		return "", nil
	}
	return "", nil
}

func marshalSnapshot(m map[string]interface{}) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func toolLogicalNames(tools []database.ToolInstance) []string {
	names := make([]string, 0, len(tools))
	for _, ti := range tools {
		name := ti.LogicalName
		if name == "" {
			name = ti.Name
		}
		names = append(names, name)
	}
	return names
}

// Create inserts a new pending proposal. Supported args: kind*, title*,
// proposed_content* (object), reasoning, target_ref (required for *_update
// kinds), source_incident_uuids (array of incident UUID strings).
// incidentID (from X-Incident-ID) is stamped as evaluation_run_uuid.
func (t *ProposalsTool) Create(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
	kind, _ := args["kind"].(string)
	if !validKinds[kind] {
		return nil, fmt.Errorf("kind must be one of: runbook_new, runbook_update, memory_new, memory_update, cron_new, cron_update, skill_prompt_update")
	}

	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	reasoning, _ := args["reasoning"].(string)

	content, ok := args["proposed_content"].(map[string]interface{})
	if !ok {
		return nil, errors.New("proposed_content must be a JSON object")
	}
	if err := validateContent(kind, content); err != nil {
		return nil, err
	}

	targetRef, _ := args["target_ref"].(string)
	targetRef = strings.TrimSpace(targetRef)
	snapshot := ""
	if isUpdateKind(kind) {
		if targetRef == "" {
			return nil, fmt.Errorf("target_ref is required for %s proposals", kind)
		}
		// skill_prompt_update embeds the skill name twice; keep them consistent.
		if kind == kindSkillPromptUpdate {
			if sn, _ := content["skill_name"].(string); sn != targetRef {
				return nil, fmt.Errorf("proposed_content.skill_name (%q) must match target_ref (%q)", content["skill_name"], targetRef)
			}
		}
		var err error
		snapshot, err = t.snapshotForTarget(ctx, kind, targetRef)
		if err != nil {
			return nil, err
		}
	} else {
		targetRef = ""
	}

	// Server-side dedup against pending proposals: same (kind, target_ref)
	// for update kinds, same kind + case-insensitive title for new kinds.
	var existing database.Proposal
	dedupQuery := t.db.WithContext(ctx).Where("status = ? AND kind = ?", statusPending, kind)
	if isUpdateKind(kind) {
		dedupQuery = dedupQuery.Where("target_ref = ?", targetRef)
	} else {
		dedupQuery = dedupQuery.Where("LOWER(title) = ?", strings.ToLower(title))
	}
	if err := dedupQuery.First(&existing).Error; err == nil {
		b, _ := json.Marshal(map[string]interface{}{
			"deduplicated": true,
			"uuid":         existing.UUID,
			"note":         "a pending proposal for this target already exists; refine it via proposals.update_draft instead",
		})
		return string(b), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Hallucination guard: keep only source incident UUIDs that exist.
	sourceUUIDs, dropped := t.filterSourceIncidentUUIDs(ctx, args["source_incident_uuids"])

	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}

	row := &database.Proposal{
		UUID:                uuid.New().String(),
		Kind:                kind,
		Status:              statusPending,
		Title:               title,
		Reasoning:           reasoning,
		TargetRef:           targetRef,
		CurrentSnapshot:     snapshot,
		ProposedContent:     string(contentJSON),
		SourceIncidentUUIDs: database.JSONB{"uuids": sourceUUIDs},
		EvaluationRunUUID:   incidentID,
		CreatedBy:           "evaluator",
	}
	if err := t.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}

	resp := map[string]interface{}{
		"deduplicated": false,
		"uuid":         row.UUID,
		"status":       row.Status,
	}
	if len(dropped) > 0 {
		resp["dropped_source_incident_uuids"] = dropped
		resp["warning"] = "some source incident UUIDs were not found and were dropped"
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// filterSourceIncidentUUIDs validates the supplied incident UUIDs against the
// incidents table, returning (kept, dropped). A nil/malformed arg yields two
// empty slices.
func (t *ProposalsTool) filterSourceIncidentUUIDs(ctx context.Context, v interface{}) ([]interface{}, []string) {
	arr, ok := v.([]interface{})
	if !ok || len(arr) == 0 {
		return []interface{}{}, nil
	}
	candidates := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			candidates = append(candidates, strings.TrimSpace(s))
		}
	}
	if len(candidates) == 0 {
		return []interface{}{}, nil
	}
	var found []string
	if err := t.db.WithContext(ctx).Model(&database.Incident{}).
		Where("uuid IN ?", candidates).Pluck("uuid", &found).Error; err != nil {
		t.logger.Printf("proposals: source incident validation failed: %v", err)
		return []interface{}{}, candidates
	}
	foundSet := make(map[string]bool, len(found))
	for _, u := range found {
		foundSet[u] = true
	}
	kept := make([]interface{}, 0, len(candidates))
	var dropped []string
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		seen[c] = true
		if foundSet[c] {
			kept = append(kept, c)
		} else {
			dropped = append(dropped, c)
		}
	}
	return kept, dropped
}

// proposalSummary is the list-view projection (no content bodies, to keep the
// evaluator's context small during dedup checks).
type proposalSummary struct {
	UUID              string `json:"uuid"`
	Kind              string `json:"kind"`
	Status            string `json:"status"`
	Title             string `json:"title"`
	TargetRef         string `json:"target_ref,omitempty"`
	EvaluationRunUUID string `json:"evaluation_run_uuid,omitempty"`
	CreatedAt         string `json:"created_at"`
}

// List returns proposals filtered by status (default "pending") and kind.
// Args: status, kind, limit (default 50, max 200), offset, include_content
// (bool — adds reasoning/current_snapshot/proposed_content to each row).
func (t *ProposalsTool) List(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	limit := defaultLimit
	if v, ok := args["limit"]; ok {
		limit = toInt(v, defaultLimit)
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset := 0
	if v, ok := args["offset"]; ok {
		offset = toInt(v, 0)
	}
	if offset < 0 {
		offset = 0
	}

	status := statusPending
	if v, ok := args["status"].(string); ok && v != "" {
		status = v
	}

	q := t.db.WithContext(ctx).Model(&database.Proposal{}).Where("status = ?", status)
	if v, ok := args["kind"].(string); ok && v != "" {
		q = q.Where("kind = ?", v)
	}

	var rows []database.Proposal
	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, err
	}

	includeContent, _ := args["include_content"].(bool)
	items := make([]interface{}, 0, len(rows))
	for _, r := range rows {
		if includeContent {
			items = append(items, proposalDetail(r))
		} else {
			items = append(items, proposalSummary{
				UUID:              r.UUID,
				Kind:              r.Kind,
				Status:            string(r.Status),
				Title:             r.Title,
				TargetRef:         r.TargetRef,
				EvaluationRunUUID: r.EvaluationRunUUID,
				CreatedAt:         r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
	}

	b, err := json.Marshal(map[string]interface{}{
		"proposals": items,
		"count":     len(items),
		"limit":     limit,
		"offset":    offset,
	})
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// proposalDetail renders the full row with proposed_content and snapshot
// decoded back into objects for agent readability.
func proposalDetail(r database.Proposal) map[string]interface{} {
	d := map[string]interface{}{
		"uuid":                  r.UUID,
		"kind":                  r.Kind,
		"status":                r.Status,
		"title":                 r.Title,
		"reasoning":             r.Reasoning,
		"target_ref":            r.TargetRef,
		"source_incident_uuids": r.SourceIncidentUUIDs,
		"evaluation_run_uuid":   r.EvaluationRunUUID,
		"created_by":            r.CreatedBy,
		"created_at":            r.CreatedAt,
		"updated_at":            r.UpdatedAt,
	}
	var content interface{}
	if err := json.Unmarshal([]byte(r.ProposedContent), &content); err == nil {
		d["proposed_content"] = content
	} else {
		d["proposed_content"] = r.ProposedContent
	}
	if r.CurrentSnapshot != "" {
		var snap interface{}
		if err := json.Unmarshal([]byte(r.CurrentSnapshot), &snap); err == nil {
			d["current_snapshot"] = snap
		} else {
			d["current_snapshot"] = r.CurrentSnapshot
		}
	}
	return d
}

// Get returns the full proposal by uuid.
func (t *ProposalsTool) Get(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	uuidStr, _ := args["uuid"].(string)
	if uuidStr == "" {
		return nil, errors.New("uuid is required")
	}
	var row database.Proposal
	if err := t.db.WithContext(ctx).Where("uuid = ?", uuidStr).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("proposal not found")
		}
		return nil, err
	}
	b, err := json.Marshal(proposalDetail(row))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// UpdateDraft revises a pending proposal's title, reasoning, and/or
// proposed_content. Non-pending proposals are immutable.
func (t *ProposalsTool) UpdateDraft(ctx context.Context, _ string, args map[string]interface{}) (interface{}, error) {
	uuidStr, _ := args["uuid"].(string)
	if uuidStr == "" {
		return nil, errors.New("uuid is required")
	}

	var row database.Proposal
	if err := t.db.WithContext(ctx).Where("uuid = ?", uuidStr).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("proposal not found")
		}
		return nil, err
	}
	if string(row.Status) != statusPending {
		return nil, fmt.Errorf("proposal is %s; only pending proposals can be revised", row.Status)
	}

	updates := map[string]interface{}{}
	if v, ok := args["title"].(string); ok {
		if strings.TrimSpace(v) == "" {
			return nil, errors.New("title must be a non-empty string when provided")
		}
		updates["title"] = strings.TrimSpace(v)
	}
	if v, ok := args["reasoning"].(string); ok {
		updates["reasoning"] = v
	}
	if v, ok := args["proposed_content"]; ok {
		content, ok := v.(map[string]interface{})
		if !ok {
			return nil, errors.New("proposed_content must be a JSON object")
		}
		if err := validateContent(row.Kind, content); err != nil {
			return nil, err
		}
		if row.Kind == kindSkillPromptUpdate {
			if sn, _ := content["skill_name"].(string); sn != row.TargetRef {
				return nil, fmt.Errorf("proposed_content.skill_name (%q) must match the proposal's target_ref (%q)", sn, row.TargetRef)
			}
		}
		b, err := json.Marshal(content)
		if err != nil {
			return nil, err
		}
		updates["proposed_content"] = string(b)
	}
	if len(updates) == 0 {
		return nil, errors.New("nothing to update: provide title, reasoning, and/or proposed_content")
	}

	if err := t.db.WithContext(ctx).Model(&database.Proposal{}).
		Where("uuid = ? AND status = ?", uuidStr, statusPending).
		Updates(updates).Error; err != nil {
		return nil, err
	}

	if err := t.db.WithContext(ctx).Where("uuid = ?", uuidStr).First(&row).Error; err != nil {
		return nil, err
	}
	b, err := json.Marshal(proposalDetail(row))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// ListCronJobs returns a read-only projection of all cron jobs so agents can
// inspect current definitions before proposing cron changes.
func (t *ProposalsTool) ListCronJobs(ctx context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	var jobs []database.CronJob
	if err := t.db.WithContext(ctx).Preload("Tools").Order("name ASC").Find(&jobs).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]interface{}, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, map[string]interface{}{
			"uuid":               j.UUID,
			"name":               j.Name,
			"schedule":           j.Schedule,
			"prompt":             j.Prompt,
			"enabled":            j.Enabled,
			"is_system":          j.IsSystem,
			"tool_logical_names": toolLogicalNames(j.Tools),
		})
	}
	b, err := json.Marshal(map[string]interface{}{
		"cron_jobs": items,
		"count":     len(items),
	})
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// toInt safely extracts an int from interface{}, returning def on failure.
func toInt(v interface{}, def int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return def
}
