---
paths:
  - "**/internal/database/models_proposals.go"
  - "**/internal/services/proposal_*"
  - "**/internal/handlers/api_proposals.go"
  - "**/mcp-gateway/internal/tools/proposals/**"
  - "**/web/src/pages/Proposal*"
---

# Self-improving proposals

The `improvement-evaluator` system cron reviews recent incidents + operator feedback and emits
`Proposal` rows (kinds: `runbook_new/update`, `memory_new/update`, `cron_new/update`,
`skill_prompt_update`) via the credential-less `proposals` gateway tool. Operators review them in the
Proposals tab, refine via chat, and approve (auto-apply) or reject.

- statuses: `pending | approved | rejected | apply_failed | superseded`; chat never changes status;
  re-approving `apply_failed` retries
- `ProposalService.Approve` applies through the existing managers (`RunbookManager`,
  `MemoryManager.UpsertByName`, `CronJobManager`, `SkillManager.UpdateSkillPrompt`) — never raw DB
  writes; status write last so a failed apply never yields an approved row
- staleness: gateway captures `current_snapshot` at create (runbook/memory/cron); skill prompts
  backfilled lazily by the API on first list/get (disk-only in the API container); approve compares
  live vs snapshot, mismatch → `superseded` + `ErrProposalStale` (409)
- `skill_prompt_update` targets non-system skills ONLY (enforced at gateway create AND apply —
  `UpdateSkillPrompt` silently no-ops for system skills); `cron_new` applies `Enabled=false`, no channel
- proposal chat = fresh `StartIncident` per turn on the same chat incident (`source_kind="proposal"`,
  root skill `proposal-editor`), NEVER `ContinueIncident`; proposal state + transcript rebuilt into
  each task; transcript in `proposal_chat_messages` written by the handler; allowlist =
  `ChatToolAllowlist()` (incidents+proposals; non-nil empty on failure); no `executor.PrependGuidance`
- `SeedImprovementEvaluatorCron()` runs from `main.go` AFTER `EnsureToolTypes()` (attaches the seeded
  tool instances); seeds disabled, same preserve-edits/shadow-check semantics as `SeedSystemCronJobs`
- gateway `proposals.create` dedups pending rows (kind+target_ref, or kind+title for `*_new`) and
  drops hallucinated `source_incident_uuids`; `proposals` is built-in + credentialless
- incident feedback UI posts to `POST /api/incidents/{uuid}/feedback`; feedback memories feed the
  next evaluator run

Key files: `internal/database/models_proposals.go`, `internal/services/proposal_service.go`,
`internal/handlers/api_proposals.go`, `mcp-gateway/internal/tools/proposals/`,
`web/src/pages/Proposals.tsx` + `ProposalDetail.tsx`
