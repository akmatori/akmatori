# Akmatori Deep Refactoring Plan

## Context

The Akmatori codebase has accumulated structural debt across all three stacks. Several god objects (1,000+ line files with 19-34 methods), 393 unstructured `log.Printf` calls, dead feature code, and duplicated React patterns are slowing development, making testing painful (handlers at 10.2% coverage), and causing unexpected side effects from tight coupling. This refactoring addresses all of it in a single sweep.

**Decisions**: Full stack | Big bang | slog (stdlib) | Custom hooks only (no new deps) | Remove dead features completely

---

## Phase 0: Dead Code Removal

Remove unimplemented features and legacy code first to reduce the surface area for subsequent phases.

### DELETE files:
- `internal/services/device_auth_service.go`
- `internal/services/device_auth_service_test.go`
- `internal/services/device_auth_service_edge_test.go`

### MODIFY files:

**`internal/handlers/api.go`**:
- Remove `codexWSHandler *CodexWSHandler` field (line 21)
- Remove `deviceAuthService *services.DeviceAuthService` field (line 23)
- Remove `deviceAuthService: services.NewDeviceAuthService()` from `NewAPIHandler` (line 39)

**`internal/handlers/api_settings.go`**:
- Remove 4 device auth handlers: `handleDeviceAuthStart`, `handleDeviceAuthStatus`, `handleDeviceAuthCancel`, `handleChatGPTDisconnect` (all `//lint:ignore U1000`)

**`internal/handlers/codex_ws.go`**:
- Remove `DeviceAuthResult` type, `DeviceAuthCallback` type, `deviceAuthCallback` field
- Remove `handleDeviceAuthResponse`, `StartDeviceAuth`, `CancelDeviceAuth` methods
- Remove device-auth `CodexMessageType` constants
- Keep all other CodexWSHandler functionality (incident execution still used)

**`internal/handlers/api_incidents.go`**:
- Remove `runIncidentLocal` (line 207, `//lint:ignore U1000`)

**`internal/handlers/alert.go`**:
- Remove `runSlackChannelInvestigationLocal` (line 1022, `//lint:ignore U1000`)

**`internal/database/models.go`**:
- Remove `OpenAISettings` struct and all its methods (`IsConfigured`, `IsActive`, `IsChatGPTTokenExpired`, `GetValidReasoningEfforts`, `ValidateReasoningEffort`, `TableName`)

**`internal/database/db.go`**:
- Remove `GetOpenAISettings`, `UpdateOpenAISettings`, `UpdateOpenAIChatGPTTokens`, `ClearOpenAIChatGPTTokens`

**`internal/database/models_test.go`**:
- Remove `BenchmarkOpenAISettings_IsConfigured` and `BenchmarkOpenAISettings_ValidateReasoningEffort`

**`internal/handlers/api_handler_test.go`**:
- Update `NewAPIHandler` calls if constructor signature changes

**Verify**: `make verify`

---

## Phase 1: Structured Logging Migration (slog)

Replace 393 `log.Printf/Fatalf/Println` calls across 28 files with `log/slog`.

### CREATE:
- **`internal/logging/logging.go`** — slog initialization (JSON handler to stderr, INFO level default)

### Transformation rules:

| Old | New | Level |
|-----|-----|-------|
| `log.Printf("Starting %s", name)` | `slog.Info("starting", "component", name)` | Info |
| `log.Printf("Warning: %v", err)` | `slog.Warn("operation failed", "error", err)` | Warn |
| `log.Printf("Error: %v", err)` / `log.Fatalf(...)` | `slog.Error("...", "error", err)` | Error |
| Progress/trace messages | `slog.Debug(...)` | Debug |

### MODIFY (28 files, by occurrence count):
1. `internal/handlers/alert.go` (66)
2. `internal/handlers/slack.go` (52)
3. `cmd/akmatori/main.go` (49) — also add `logging.Init()` call
4. `internal/executor/codex.go` (39)
5. `internal/handlers/api_incidents.go` (21)
6. `internal/slack/manager.go` (20)
7. `internal/handlers/codex_ws.go` (18)
8. `internal/services/skill_service.go` (18)
9. `internal/database/db.go` (16)
10. `internal/handlers/agent_ws.go` (13)
11. `internal/jobs/recorrelation.go` (13)
12. `internal/alerts/extraction/extractor.go` (11)
13. Remaining 16 files (1-8 each)

Also update `mcp-gateway/` (8 occurrences across 2 files).

**Verify**: `make verify` + `grep -r 'log\.Printf\|log\.Fatalf\|log\.Println' internal/ cmd/ --include='*.go'` returns 0

---

## Phase 2: Go Backend — Split God Objects

### 2A: Split `internal/database/models.go` (588 lines → 6 files)

| New file | Content |
|----------|---------|
| `models.go` | Keep only `JSONB` type + Scan/Value |
| `models_settings.go` | SlackSettings, LLMSettings, LLMProvider, ThinkingLevel, ProxySettings, GeneralSettings, APIKeySettings, APIKeyEntry |
| `models_skills.go` | Skill, ToolType, ToolInstance, SkillTool, EventSource, EventSourceType |
| `models_alerts.go` | AlertSourceType, AlertSourceInstance, AlertSeverity, AlertStatus, GetSeverityEmoji |
| `models_incidents.go` | Incident, IncidentStatus constants |
| `models_context.go` | ContextFile, Runbook |

All files: `package database`. No import changes anywhere — purely file-level split.

### 2B: Split `internal/services/skill_service.go` (1,129 lines → 4 files)

| New file | Methods moved |
|----------|-------------|
| `skill_file_sync.go` | EnsureSkillDirectories, EnsureSkillScriptsDir, SyncSkillAssets, ClearSkillScripts, ListSkillScripts, GetSkillScript, UpdateSkillScript, DeleteSkillScript, SyncSkillsFromFilesystem, RegenerateAllSkillMds |
| `skill_prompt_service.go` | generateSkillMd, generateToolUsageExample, extractToolDetails, sshAllHostsAllowWrite, GetSkillPrompt, UpdateSkillPrompt, stripAutoGeneratedSections, SkillFrontmatter, truncateString |
| `incident_service.go` | IncidentContext, SubagentSummaryInput, SpawnIncidentManager, generateIncidentAgentsMd, UpdateIncidentStatus, UpdateIncidentComplete, UpdateIncidentLog, GetIncident, SummarizeSubagentForContext, AppendSubagentLog |
| `skill_service.go` (keep) | SkillService struct, NewSkillService, CRUD ops, AssignTools, validation |

All methods stay on `*SkillService` receiver — same package, just different files.

### 2C: Split `internal/handlers/alert.go` (1,272 lines → 4 files)

| New file | Methods moved |
|----------|-------------|
| `alert_processor.go` | processAlert, processSlackChannelAlert, buildAlertTask, buildAlertHeader |
| `alert_slack.go` | postSlackThreadReply, postSlackThreadReplyGetTS, updateSlackThreadMessage, updateSlackChannelReactions, buildSlackResponse, buildSlackNotification, formatSlackAlertMessage, truncateLogForSlack |
| `alert_aggregation.go` | checkForExistingIncident, tryAttachToIncident, buildAggregationContext |
| `alert.go` (keep) | AlertHandler struct, NewAlertHandler, RegisterAdapter, HandleWebhook, ServeHTTP, runInvestigation |

### 2D: Split `internal/handlers/slack.go` (909 lines → 3 files)

| New file | Content |
|----------|---------|
| `slack_processor.go` | Business logic extracted from event handling methods |
| `slack_formatting.go` | Message formatting utilities |
| `slack.go` (keep) | SlackHandler struct, NewSlackHandler, HandleSocketMode, event dispatch |

### 2E: Split `internal/handlers/api_settings.go` (586 lines → 4 files)

| New file | Content |
|----------|---------|
| `api_settings_llm.go` | handleLLMSettings, ModelConfigs |
| `api_settings_slack.go` | handleSlackSettings, maskToken |
| `api_settings_proxy.go` | handleProxySettings, GetProxySettings, UpdateProxySettings, maskProxyURL, isValidURL |
| `api_settings_general.go` | handleGeneralSettings, handleGetAggregationSettings, handleUpdateAggregationSettings |

Delete `api_settings.go` after all content moved.

**Verify after each sub-phase**: `make verify`

---

## Phase 3: Interface Extraction for Testability

### CREATE:
- **`internal/services/interfaces.go`** — Define interfaces:
  - `SkillManager` — skill CRUD + lifecycle
  - `IncidentManager` — incident spawn/update/get
  - `ToolManager` — tool instance CRUD
  - `AlertManager` — alert source operations
  - `RunbookManager` — runbook CRUD

### MODIFY:
- **`internal/handlers/api.go`** — Change `APIHandler` struct fields from `*services.XxxService` to `services.XxxManager` interfaces
- **`internal/handlers/alert.go`** — Change `AlertHandler` fields similarly
- **`cmd/akmatori/main.go`** — No changes needed (concrete types satisfy interfaces via Go structural typing)

This unblocks future handler unit tests with mock services.

**Verify**: `make verify`

---

## Phase 4: React Frontend Refactoring

### 4A: Create shared hooks

| New file | Purpose |
|----------|---------|
| `web/src/hooks/useAsync.ts` | Generic loading/error/data hook — replaces duplicated pattern in 11+ components |
| `web/src/hooks/useFormState.ts` | Consolidated form state management |

### 4B: Split `web/src/pages/Tools.tsx` (993 lines)

| New file | Content |
|----------|---------|
| `web/src/hooks/useToolManagement.ts` | Tool CRUD state + handlers |
| `web/src/hooks/useSSHKeyManagement.ts` | SSH key state + handlers |
| `web/src/components/tools/ToolFormSection.tsx` | Tool create/edit form |
| `web/src/components/tools/SSHKeysSection.tsx` | SSH key management UI |
| `web/src/components/tools/SSHHostsSection.tsx` | SSH host config UI |

`Tools.tsx` becomes thin orchestrator importing these.

### 4C: Split `web/src/pages/Settings.tsx` (792 lines)

| New file | Content |
|----------|---------|
| `web/src/components/settings/LLMSettingsSection.tsx` | Provider, model, API key config |
| `web/src/components/settings/SlackSettingsSection.tsx` | Slack token management |
| `web/src/components/settings/GeneralSettingsSection.tsx` | Base URL, general config |

### 4D: Split `web/src/components/AlertSourcesManager.tsx` (595 lines)

| New file | Content |
|----------|---------|
| `web/src/hooks/useAlertSourceManagement.ts` | Alert source CRUD + webhook logic |
| `web/src/components/alerts/AlertSourceForm.tsx` | Create/edit form |

### 4E: Shared components

| New file | Content |
|----------|---------|
| `web/src/components/shared/StatusBadge.tsx` | Unified status/severity badge (currently duplicated in 3+ files) |
| `web/src/components/shared/LoadingError.tsx` | Loading spinner + error message compound component |

**Verify**: `make test-agent` + manual browser check

---

## Phase 5: Agent Worker Refactoring

### CREATE:
- **`agent-worker/src/tool-output-formatter.ts`** — Extract from `agent-runner.ts`:
  - `formatToolArgs`, `formatToolOutput`, `extractToolText`, `collectTextParts`, `collectContentText`, `safeJSONStringify`
  - `ToolExecutionTrace` interface

### MODIFY:
- **`agent-worker/src/agent-runner.ts`** — Import formatter, delegate, remove ~130 lines

**Verify**: `cd agent-worker && npm test`

---

## Phase 6: Final Validation

1. Remove any remaining `//lint:ignore U1000` or `//nolint:unused` on deleted code
2. Run full suite:
   ```bash
   make verify          # go vet + Go tests
   make test-all        # includes agent-worker
   golangci-lint run    # lint pass
   ```
3. Build all Docker containers:
   ```bash
   docker-compose build
   ```
4. Manual smoke test: start stack, create incident, verify Slack flow

---

## Execution Order & Dependencies

```
Phase 0 (dead code) → Phase 1 (slog) → Phase 2 (file splits) → Phase 3 (interfaces)
                                                                       ↓
                                                    Phase 4 (frontend) + Phase 5 (agent)  [parallel]
                                                                       ↓
                                                                 Phase 6 (validation)
```

Phases 4 and 5 are independent and can run in parallel. Everything else is sequential.

---

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| File split breaks compilation | All splits stay within same Go package — no import changes |
| Interface extraction breaks wiring | Go structural typing = concrete types auto-satisfy interfaces |
| slog changes log output format | JSON is more parseable; any log grep scripts need updating |
| Frontend split breaks UI state | No logic changes — only extraction; manual browser verification |
| Dead code removal hits live reference | `make verify` catches immediately; grep verified all references |

**Rollback**: Each phase is a separate commit. `git revert` any phase if needed. No database migrations involved.
