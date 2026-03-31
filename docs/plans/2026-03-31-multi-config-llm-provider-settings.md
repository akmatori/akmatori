# Multi-Config LLM Provider Settings

## Overview

Allow multiple LLM configurations per provider type (e.g., two OpenAI setups with different models/keys). Only one configuration is globally active at a time, but users can quickly switch between saved configs.

## Context

- Files involved:
  - `internal/database/models_settings.go` - LLMSettings model (uniqueIndex on Provider)
  - `internal/database/db.go` - DB operations (GetLLMSettings, GetAllLLMSettings, SetActiveLLMProvider, seedLLMProviders)
  - `internal/handlers/api_settings_llm.go` - HTTP handler (GET/PUT /api/settings/llm)
  - `internal/api/types.go` - UpdateLLMSettingsRequest
  - `internal/handlers/api_incidents.go` - BuildLLMSettingsForWorker
  - `internal/handlers/agent_ws.go` - LLMSettingsForWorker struct
  - `web/src/components/settings/LLMSettingsSection.tsx` - Frontend UI
  - `web/src/api.ts` - Frontend API client types
  - `internal/testhelpers/builders.go` - LLMSettingsBuilder
- Related patterns: REST CRUD following existing tool_instances/runbooks patterns
- Dependencies: None external

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Update database model and migration

**Files:**
- Modify: `internal/database/models_settings.go`
- Modify: `internal/database/db.go`

- [x] Remove `uniqueIndex` from Provider field on LLMSettings, replace with regular index
- [x] Add `Name` field (varchar 100, not null) with uniqueIndex to LLMSettings model
- [x] Update `seedLLMProviders()` to set Name from provider display name (e.g., "OpenAI", "Anthropic") for initial seed rows
- [x] Add migration logic in `AutoMigrate` to populate Name for existing rows that have empty Name (set Name = titlecase of Provider value)
- [x] Replace `GetLLMSettingsByProvider(provider)` with `GetLLMSettingsByID(id uint)` function
- [x] Update `GetAllLLMSettings()` to order by provider, then name
- [x] Replace `SetActiveLLMProvider(provider)` with `SetActiveLLMConfig(id uint)` that deactivates all rows and activates the row with given ID
- [x] Add `DeleteLLMSettings(id uint)` that prevents deleting the active config
- [x] Add `CreateLLMSettings(settings *LLMSettings)` function
- [x] Update `GetLLMSettings()` (active config fetcher) - no structural changes needed, it already returns the active row
- [x] Update LLMSettingsBuilder in `internal/testhelpers/builders.go` to include Name field
- [x] Write tests for new DB operations (create, delete, activate by ID, name uniqueness)
- [x] Run `make test`

### Task 2: Update API types and handler

**Files:**
- Modify: `internal/api/types.go`
- Modify: `internal/handlers/api_settings_llm.go`
- Modify: `internal/handlers/routes.go` (if routes are defined here)

- [ ] Add `CreateLLMSettingsRequest` struct with fields: Provider (required), Name (required), APIKey, Model, ThinkingLevel, BaseURL
- [ ] Update `UpdateLLMSettingsRequest` to include optional Name field
- [ ] Refactor handler to support new REST endpoints:
  - `GET /api/settings/llm` - list all configs (returns array of configs + which ID is active)
  - `POST /api/settings/llm` - create new config (validates name uniqueness, provider validity)
  - `GET /api/settings/llm/{id}` - get single config
  - `PUT /api/settings/llm/{id}` - update config (partial update, validate name uniqueness if changed)
  - `DELETE /api/settings/llm/{id}` - delete config (reject if active, reject if it is the last config)
  - `PUT /api/settings/llm/{id}/activate` - set config as globally active
- [ ] GET list response format: `{"configs": [...], "active_id": 3}` where each config includes id, name, provider, model, thinking_level, base_url, is_configured, masked api_key, enabled, created_at, updated_at
- [ ] Maintain backward compatibility: keep existing `GET /api/settings/llm` working but with new response shape
- [ ] API key masking on all responses (same as current)
- [ ] Write handler tests covering: list, create, update, delete, activate, validation errors, name conflicts
- [ ] Run `make test`

### Task 3: Update incident LLM settings resolution

**Files:**
- Modify: `internal/handlers/api_incidents.go`

- [ ] Verify `GetLLMSettings()` still works correctly for incident creation (it should - it returns the active row regardless of provider)
- [ ] Verify `BuildLLMSettingsForWorker()` still works (no changes expected - it reads from the active config)
- [ ] Write/update tests to confirm incident creation works when active config is any provider with any name
- [ ] Run `make test`

### Task 4: Update frontend UI

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/components/settings/LLMSettingsSection.tsx`

- [ ] Update TypeScript types: add LLMConfig interface (id, name, provider, model, etc.), update LLMSettingsListResponse with configs array and active_id
- [ ] Update API client: add methods for create, update, delete, activate, update list endpoint return type
- [ ] Redesign LLMSettingsSection: replace provider-tab layout with a config list/card view
  - Show all saved configs as cards grouped by provider, with active indicator
  - Each card shows: name, provider badge, model, configured status
  - "Activate" button on each card (disabled on already-active)
  - "Edit" button opens edit form (name, provider read-only after creation, api_key, model, thinking_level, base_url)
  - "Delete" button with confirmation (disabled on active config)
  - "Add Configuration" button opens create form (provider dropdown, name, api_key, model, thinking_level, base_url)
- [ ] Model suggestions dropdown should still work based on provider type of the config being edited
- [ ] Verify the active config is visually distinct (highlighted border, badge, or similar)
- [ ] Test frontend manually: create, edit, delete, switch active configs

### Task 5: Verify acceptance criteria

- [ ] Run full test suite: `make verify`
- [ ] Run linter: `golangci-lint run`
- [ ] Multiple configs per provider can be created via API
- [ ] Only one config is globally active at a time
- [ ] Switching active config works and is reflected in new incidents
- [ ] Deleting the active config is prevented
- [ ] Frontend shows all configs and allows CRUD + switching

### Task 6: Update documentation

- [ ] Update CLAUDE.md if internal patterns changed (new API endpoints, model changes)
- [ ] Move this plan to `docs/plans/completed/`
