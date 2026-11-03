package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// ErrProposalNotFound is returned when the referenced proposal row is absent.
var ErrProposalNotFound = errors.New("proposal not found")

// ErrProposalNotApprovable is returned when approve/reject hits a proposal
// whose status forbids the transition. Handlers map it to HTTP 409.
var ErrProposalNotApprovable = errors.New("proposal is not in an approvable state")

// ErrProposalStale is returned when the live target changed since the
// proposal's snapshot was captured. The proposal is marked superseded before
// this error is returned; handlers map it to HTTP 409.
var ErrProposalStale = errors.New("proposal target changed since the proposal was created")

// proposalRunbookContent / proposalMemoryContent / proposalCronContent /
// proposalSkillPromptContent are the per-kind JSON documents stored in
// Proposal.ProposedContent and Proposal.CurrentSnapshot. The gateway
// validates the same shapes at creation time.
type proposalRunbookContent struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type proposalMemoryContent struct {
	Scope       string `json:"scope"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	// Evidence fields. The proposal is model-written, so approval applies
	// the agent rules (no confirmed, causes unverified). In a snapshot they
	// are pointers: a snapshot taken before evidence grades has none, and
	// then they are not compared.
	Evidence     *string `json:"evidence,omitempty"`
	Claim        *string `json:"claim,omitempty"`
	EvidenceNote *string `json:"evidence_note,omitempty"`
}

// strValue returns *p, or "" for nil.
func strValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// proposalCronContent carries the cron's tool access as tools_enabled +
// builtin_tools. Proposals created before the tool mapping was removed carry
// tool_logical_names instead; translateLegacyCronProposal rewrites them.
type proposalCronContent struct {
	Name             string   `json:"name"`
	Schedule         string   `json:"schedule"`
	Prompt           string   `json:"prompt"`
	ToolsEnabled     *bool    `json:"tools_enabled,omitempty"`
	BuiltinTools     []string `json:"builtin_tools,omitempty"`
	ToolLogicalNames []string `json:"tool_logical_names,omitempty"`
}

type proposalSkillPromptContent struct {
	SkillName string `json:"skill_name"`
	Prompt    string `json:"prompt"`
}

// ProposalService implements ProposalManager: listing/reading proposals,
// deterministic apply-on-approve through the existing managers, staleness
// detection, and the chat transcript store. Wired in main.go and consumed by
// handlers through the ProposalManager interface.
type ProposalService struct {
	db       *gorm.DB
	runbooks RunbookManager
	memories MemoryManager
	crons    CronJobManager
	skills   SkillManager
}

// NewProposalService constructs a ProposalService bound to the global DB.
func NewProposalService(db *gorm.DB, runbooks RunbookManager, memories MemoryManager, crons CronJobManager, skills SkillManager) *ProposalService {
	return &ProposalService{db: db, runbooks: runbooks, memories: memories, crons: crons, skills: skills}
}

// ListProposals returns proposals filtered by status and kind (either may be
// empty), newest first, with the total count for pagination. Pending
// skill_prompt_update rows get their CurrentSnapshot backfilled lazily —
// skill prompts live on disk in the API container, so the gateway could not
// capture them at creation time.
func (s *ProposalService) ListProposals(status, kind string, limit, offset int) ([]database.Proposal, int64, error) {
	q := s.db.Model(&database.Proposal{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count proposals: %w", err)
	}

	var rows []database.Proposal
	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list proposals: %w", err)
	}
	for i := range rows {
		s.backfillSkillSnapshot(&rows[i])
		s.translateLegacyCronProposal(&rows[i])
	}
	return rows, total, nil
}

// GetProposal returns a single proposal by UUID (with lazy skill snapshot
// backfill, same as ListProposals).
func (s *ProposalService) GetProposal(uuid string) (*database.Proposal, error) {
	var row database.Proposal
	if err := s.db.Where("uuid = ?", uuid).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProposalNotFound
		}
		return nil, err
	}
	s.backfillSkillSnapshot(&row)
	s.translateLegacyCronProposal(&row)
	return &row, nil
}

// CountPending returns the number of pending proposals (nav badge).
func (s *ProposalService) CountPending() (int64, error) {
	var n int64
	err := s.db.Model(&database.Proposal{}).
		Where("status = ?", database.ProposalStatusPending).Count(&n).Error
	return n, err
}

// backfillSkillSnapshot persists the live skill prompt as CurrentSnapshot for
// pending skill_prompt_update proposals that don't have one yet. Best-effort:
// a read failure leaves the snapshot empty and is retried on the next read.
func (s *ProposalService) backfillSkillSnapshot(p *database.Proposal) {
	if p.Kind != database.ProposalKindSkillPromptUpdate || p.CurrentSnapshot != "" ||
		p.Status != database.ProposalStatusPending || s.skills == nil {
		return
	}
	prompt, err := s.skills.GetSkillPrompt(p.TargetRef)
	if err != nil {
		slog.Debug("proposal skill snapshot backfill skipped", "proposal", p.UUID, "skill", p.TargetRef, "err", err)
		return
	}
	b, err := json.Marshal(proposalSkillPromptContent{SkillName: p.TargetRef, Prompt: prompt})
	if err != nil {
		return
	}
	p.CurrentSnapshot = string(b)
	if err := s.db.Model(&database.Proposal{}).Where("uuid = ?", p.UUID).
		Update("current_snapshot", p.CurrentSnapshot).Error; err != nil {
		slog.Warn("failed to persist skill snapshot backfill", "proposal", p.UUID, "err", err)
	}
}

// Reject marks a pending (or apply_failed) proposal rejected.
func (s *ProposalService) Reject(uuid string) (*database.Proposal, error) {
	p, err := s.GetProposal(uuid)
	if err != nil {
		return nil, err
	}
	if p.Status != database.ProposalStatusPending && p.Status != database.ProposalStatusApplyFailed {
		return nil, fmt.Errorf("%w: status is %s", ErrProposalNotApprovable, p.Status)
	}
	if err := s.db.Model(&database.Proposal{}).Where("uuid = ?", uuid).
		Update("status", database.ProposalStatusRejected).Error; err != nil {
		return nil, err
	}
	p.Status = database.ProposalStatusRejected
	return p, nil
}

// Approve applies the proposal through the existing managers and transitions
// its status. Semantics:
//
//  1. Only pending or apply_failed proposals can be approved (409 otherwise);
//     re-approving an apply_failed proposal retries the apply.
//  2. The live target is compared against CurrentSnapshot; a mismatch marks
//     the proposal superseded and returns ErrProposalStale — the operator
//     reviewed content that no longer matches reality.
//  3. The apply switch calls the same services the REST API uses, so disk
//     sync, cron runner reload, and SKILL.md regeneration all behave exactly
//     as an operator-driven edit would.
//  4. Success → approved + applied_at; failure → apply_failed + apply_error.
//     The status write happens last so a failed apply never leaves an
//     approved row without its side effects.
func (s *ProposalService) Approve(ctx context.Context, uuid string) (*database.Proposal, error) {
	p, err := s.GetProposal(uuid)
	if err != nil {
		return nil, err
	}
	if p.Status != database.ProposalStatusPending && p.Status != database.ProposalStatusApplyFailed {
		return nil, fmt.Errorf("%w: status is %s", ErrProposalNotApprovable, p.Status)
	}

	if stale, reason := s.checkStale(p); stale {
		if err := s.db.Model(&database.Proposal{}).Where("uuid = ?", uuid).
			Update("status", database.ProposalStatusSuperseded).Error; err != nil {
			return nil, err
		}
		p.Status = database.ProposalStatusSuperseded
		return p, fmt.Errorf("%w: %s", ErrProposalStale, reason)
	}

	applyErr := s.apply(ctx, p)

	updates := map[string]interface{}{}
	if applyErr != nil {
		updates["status"] = database.ProposalStatusApplyFailed
		updates["apply_error"] = applyErr.Error()
	} else {
		now := time.Now()
		updates["status"] = database.ProposalStatusApproved
		updates["applied_at"] = &now
		updates["apply_error"] = ""
	}
	if err := s.db.Model(&database.Proposal{}).Where("uuid = ?", uuid).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("proposal applied (%v) but status update failed: %w", applyErr == nil, err)
	}

	refreshed, err := s.GetProposal(uuid)
	if err != nil {
		return nil, err
	}
	if applyErr != nil {
		return refreshed, applyErr
	}
	return refreshed, nil
}

// checkStale compares the live target against the proposal's snapshot.
// Returns (true, reason) when the operator-reviewed "before" no longer
// matches reality. *_new kinds check for name collisions instead — except
// memory_new, where UpsertByName is idempotent by (scope, name) and an
// existing row is simply overwritten (memory updates are cheap and the
// operator saw the full proposed body).
func (s *ProposalService) checkStale(p *database.Proposal) (bool, string) {
	switch p.Kind {
	case database.ProposalKindRunbookUpdate:
		id, err := strconv.ParseUint(p.TargetRef, 10, 64)
		if err != nil {
			return true, fmt.Sprintf("invalid runbook target_ref %q", p.TargetRef)
		}
		rb, err := s.runbooks.GetRunbook(uint(id))
		if err != nil {
			return true, fmt.Sprintf("runbook %s no longer exists", p.TargetRef)
		}
		var snap proposalRunbookContent
		if json.Unmarshal([]byte(p.CurrentSnapshot), &snap) != nil {
			return false, "" // unreadable snapshot: proceed rather than dead-end the proposal
		}
		if rb.Title != snap.Title || rb.Content != snap.Content {
			return true, "runbook content changed since the proposal was created"
		}
	case database.ProposalKindRunbookNew:
		var content proposalRunbookContent
		if json.Unmarshal([]byte(p.ProposedContent), &content) == nil && content.Title != "" {
			var n int64
			if err := s.db.Model(&database.Runbook{}).Where("LOWER(title) = ?", strings.ToLower(content.Title)).Count(&n).Error; err == nil && n > 0 {
				return true, fmt.Sprintf("a runbook titled %q already exists", content.Title)
			}
		}
	case database.ProposalKindMemoryUpdate:
		scope, name, ok := strings.Cut(p.TargetRef, "/")
		if !ok {
			return true, fmt.Sprintf("invalid memory target_ref %q", p.TargetRef)
		}
		var mem database.Memory
		if err := s.db.Where("scope = ? AND name = ?", scope, name).First(&mem).Error; err != nil {
			return true, fmt.Sprintf("memory %s no longer exists", p.TargetRef)
		}
		var snap proposalMemoryContent
		if json.Unmarshal([]byte(p.CurrentSnapshot), &snap) != nil {
			return false, ""
		}
		// The snapshot may predate the fact/feedback types; compare the
		// normalized type so the migration alone does not mark it stale.
		if mem.Body != snap.Body || mem.Description != snap.Description || NormalizeMemoryType(mem.Type) != NormalizeMemoryType(snap.Type) {
			return true, "memory content changed since the proposal was created"
		}
		if (snap.Evidence != nil && *snap.Evidence != mem.Evidence) ||
			(snap.Claim != nil && *snap.Claim != mem.Claim) ||
			(snap.EvidenceNote != nil && *snap.EvidenceNote != mem.EvidenceNote) {
			return true, "memory evidence changed since the proposal was created"
		}
	case database.ProposalKindCronUpdate:
		job, err := s.crons.GetJobByUUID(p.TargetRef)
		if err != nil {
			return true, fmt.Sprintf("cron job %s no longer exists", p.TargetRef)
		}
		var snap proposalCronContent
		if json.Unmarshal([]byte(p.CurrentSnapshot), &snap) != nil {
			return false, ""
		}
		if job.Name != snap.Name || job.Schedule != snap.Schedule || job.Prompt != snap.Prompt {
			return true, "cron job changed since the proposal was created"
		}
		if snap.ToolsEnabled != nil && (*snap.ToolsEnabled != job.ToolsEnabled || !equalStringSets(job.BuiltinTools, snap.BuiltinTools)) {
			return true, "cron job tool access changed since the proposal was created"
		}
	case database.ProposalKindCronNew:
		var content proposalCronContent
		if json.Unmarshal([]byte(p.ProposedContent), &content) == nil && content.Name != "" {
			var n int64
			if err := s.db.Model(&database.CronJob{}).Where("name = ?", content.Name).Count(&n).Error; err == nil && n > 0 {
				return true, fmt.Sprintf("a cron job named %q already exists", content.Name)
			}
		}
	case database.ProposalKindSkillPromptUpdate:
		if s.skills == nil {
			return true, "skill service not available"
		}
		livePrompt, err := s.skills.GetSkillPrompt(p.TargetRef)
		if err != nil {
			return true, fmt.Sprintf("skill %q prompt unreadable: %v", p.TargetRef, err)
		}
		if p.CurrentSnapshot == "" {
			// Never backfilled (skill service was unavailable at read time);
			// backfill happened in GetProposal above under normal operation.
			return false, ""
		}
		var snap proposalSkillPromptContent
		if json.Unmarshal([]byte(p.CurrentSnapshot), &snap) != nil {
			return false, ""
		}
		if livePrompt != snap.Prompt {
			return true, "skill prompt changed since the proposal was created"
		}
	}
	return false, ""
}

// apply executes the per-kind mutation through the existing managers.
func (s *ProposalService) apply(ctx context.Context, p *database.Proposal) error {
	switch p.Kind {
	case database.ProposalKindRunbookNew, database.ProposalKindRunbookUpdate:
		var content proposalRunbookContent
		if err := json.Unmarshal([]byte(p.ProposedContent), &content); err != nil {
			return fmt.Errorf("invalid proposed_content: %w", err)
		}
		if content.Title == "" || content.Content == "" {
			return errors.New("proposed_content must have non-empty title and content")
		}
		if p.Kind == database.ProposalKindRunbookNew {
			_, err := s.runbooks.CreateRunbook(content.Title, content.Content)
			return err
		}
		id, err := strconv.ParseUint(p.TargetRef, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid runbook target_ref %q", p.TargetRef)
		}
		_, err = s.runbooks.UpdateRunbook(uint(id), content.Title, content.Content)
		return err

	case database.ProposalKindMemoryNew, database.ProposalKindMemoryUpdate:
		var content proposalMemoryContent
		if err := json.Unmarshal([]byte(p.ProposedContent), &content); err != nil {
			return fmt.Errorf("invalid proposed_content: %w", err)
		}
		// For memory_update the (scope, name) identity comes from target_ref;
		// the content's own scope/name could have drifted during chat edits.
		if p.Kind == database.ProposalKindMemoryUpdate {
			scope, name, ok := strings.Cut(p.TargetRef, "/")
			if !ok {
				return fmt.Errorf("invalid memory target_ref %q", p.TargetRef)
			}
			content.Scope, content.Name = scope, name
		}
		mem := &database.Memory{
			Scope:        content.Scope,
			Type:         content.Type,
			Name:         content.Name,
			Description:  content.Description,
			Body:         content.Body,
			CreatedBy:    database.ProposalCreatedByOperator,
			Evidence:     strValue(content.Evidence),
			Claim:        strValue(content.Claim),
			EvidenceNote: strValue(content.EvidenceNote),
		}
		// The operator approves the change, not every claim in it: a
		// model-drafted cause stays unverified until an operator confirms
		// it on the memory itself.
		if _, err := ApplyAgentMemoryEvidence(mem); err != nil {
			return fmt.Errorf("invalid proposed_content: %w", err)
		}
		// A rewrite is not a check: content without a grade does not raise
		// an unverified memory, and content without a note keeps the note.
		var stored database.Memory
		if err := s.db.Select("evidence", "evidence_note").Where("scope = ? AND name = ?", mem.Scope, mem.Name).Take(&stored).Error; err == nil {
			if content.Evidence == nil && stored.Evidence == MemoryEvidenceUnverified {
				mem.Evidence = MemoryEvidenceUnverified
			}
			if content.EvidenceNote == nil && mem.EvidenceNote == "" {
				mem.EvidenceNote = stored.EvidenceNote
			}
		}
		// UpsertByName is idempotent by (scope, name) and syncs files itself.
		_, err := s.memories.UpsertByName(mem)
		return err

	case database.ProposalKindCronNew, database.ProposalKindCronUpdate:
		var content proposalCronContent
		if err := json.Unmarshal([]byte(p.ProposedContent), &content); err != nil {
			return fmt.Errorf("invalid proposed_content: %w", err)
		}
		toolsEnabled := content.ToolsEnabled != nil && *content.ToolsEnabled
		if p.Kind == database.ProposalKindCronNew {
			builtins := content.BuiltinTools
			if builtins == nil {
				builtins = DefaultCronBuiltinTools
			}
			// New crons apply DISABLED and without an explicit channel: an
			// LLM-authored schedule must never start firing without an
			// explicit operator enable, and channel selection stays an
			// operator decision (the runner falls back to the provider
			// default at fire time).
			_, err := s.crons.CreateJob(content.Name, content.Schedule, content.Prompt, "", false, true,
				CronJobTools{ToolsEnabled: toolsEnabled, BuiltinTools: builtins})
			return err
		}
		patch := CronJobUpdate{
			Name:     &content.Name,
			Schedule: &content.Schedule,
			Prompt:   &content.Prompt,
		}
		// Each tool field applies only when the proposal carries it: an
		// update without it leaves that part of the cron's access alone, and
		// an explicit "builtin_tools": [] removes every built-in.
		if content.ToolsEnabled != nil {
			patch.ToolsEnabled = &toolsEnabled
		}
		if content.BuiltinTools != nil {
			builtins := content.BuiltinTools
			patch.BuiltinTools = &builtins
		}
		var err error
		_, err = s.crons.UpdateJob(p.TargetRef, patch)
		return err

	case database.ProposalKindSkillPromptUpdate:
		var content proposalSkillPromptContent
		if err := json.Unmarshal([]byte(p.ProposedContent), &content); err != nil {
			return fmt.Errorf("invalid proposed_content: %w", err)
		}
		if content.Prompt == "" {
			return errors.New("proposed_content.prompt cannot be empty")
		}
		skill, err := s.skills.GetSkill(p.TargetRef)
		if err != nil {
			return fmt.Errorf("skill %q not found", p.TargetRef)
		}
		// UpdateSkillPrompt silently no-ops for system skills — reporting
		// "applied" for one would be a lie, so refuse explicitly.
		if skill.IsSystem {
			return fmt.Errorf("skill %q is a system skill; its prompt is hardcoded and cannot be changed", p.TargetRef)
		}
		// UpdateSkillPrompt regenerates the full SKILL.md itself.
		return s.skills.UpdateSkillPrompt(p.TargetRef, content.Prompt)
	}
	return fmt.Errorf("unknown proposal kind %q", p.Kind)
}

// translateLegacyCronProposal rewrites the cron content of a proposal made
// before the tool mapping was removed: tool_logical_names become
// tools_enabled + builtin_tools, with the same rule as the migration of
// cron_job_tools (database.LegacyCronToolFields). Applied in memory on every
// read, so list, approve and the staleness check all see the new shape.
func (s *ProposalService) translateLegacyCronProposal(p *database.Proposal) {
	if p.Kind != database.ProposalKindCronNew && p.Kind != database.ProposalKindCronUpdate {
		return
	}
	p.ProposedContent = s.translateLegacyCronContent(p.ProposedContent)
	p.CurrentSnapshot = s.translateLegacyCronContent(p.CurrentSnapshot)
}

func (s *ProposalService) translateLegacyCronContent(raw string) string {
	if raw == "" {
		return raw
	}
	var c proposalCronContent
	if json.Unmarshal([]byte(raw), &c) != nil || c.ToolsEnabled != nil {
		return raw
	}
	var legacy map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &legacy) != nil {
		return raw
	}
	if _, ok := legacy["tool_logical_names"]; !ok {
		return raw
	}
	types := s.toolTypesForLogicalNames(c.ToolLogicalNames)
	enabled, builtins := database.LegacyCronToolFields(types)
	c.ToolsEnabled = &enabled
	c.BuiltinTools = []string(builtins)
	c.ToolLogicalNames = nil
	b, err := json.Marshal(c)
	if err != nil {
		return raw
	}
	return string(b)
}

// toolTypesForLogicalNames resolves logical names to tool type names. A name
// that matches no instance counts as a connector tool: the reviewer saw an
// infrastructure tool there.
func (s *ProposalService) toolTypesForLogicalNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	var instances []database.ToolInstance
	if err := s.db.Preload("ToolType").Where("logical_name IN ?", names).Find(&instances).Error; err != nil {
		slog.Warn("proposal translation: resolve tool logical names", "err", err)
	}
	byName := make(map[string]string, len(instances))
	for _, ti := range instances {
		byName[ti.LogicalName] = ti.ToolType.Name
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if tt, ok := byName[n]; ok && tt != "" {
			out = append(out, tt)
		} else if database.IsBuiltinToolType(n) {
			out = append(out, n)
		} else {
			out = append(out, "unknown-connector")
		}
	}
	return out
}

// equalStringSets compares two string slices as sets (order-insensitive).
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// SetChatIncident records the agent invocation backing a proposal's chat.
func (s *ProposalService) SetChatIncident(uuid, incidentUUID string) error {
	res := s.db.Model(&database.Proposal{}).Where("uuid = ?", uuid).
		Update("chat_incident_uuid", incidentUUID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrProposalNotFound
	}
	return nil
}

// AppendChatMessage persists one chat turn (role "operator" or "assistant").
func (s *ProposalService) AppendChatMessage(proposalUUID, role, content string) error {
	return s.db.Create(&database.ProposalChatMessage{
		ProposalUUID: proposalUUID,
		Role:         role,
		Content:      content,
	}).Error
}

// ListChatMessages returns the proposal's chat transcript, oldest first.
func (s *ProposalService) ListChatMessages(proposalUUID string) ([]database.ProposalChatMessage, error) {
	var rows []database.ProposalChatMessage
	err := s.db.Where("proposal_uuid = ?", proposalUUID).
		Order("id ASC").Find(&rows).Error
	return rows, err
}

// ChatToolAllowlist is the allowlist of proposal-editor chat runs: the
// incidents, proposals, runbooks and memory built-ins (type-only entries, no
// lookup). Always non-nil.
func (s *ProposalService) ChatToolAllowlist() []ToolAllowlistEntry {
	return ProposalChatAllowlist()
}
