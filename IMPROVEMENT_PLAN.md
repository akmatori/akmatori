# Akmatori Improvement Plan

This document outlines recommended improvements for the Akmatori AIOps platform, organized by priority and area.

---

## Executive Summary

| Area | Current State | Key Gaps |
|------|---------------|----------|
| Frontend | Well-structured React 19 app | No testing, manual state management, no caching |
| Backend API | Good separation of concerns | 0% handler/service tests, poor error handling |
| MCP Gateway | Strong security foundation | Insecure SSH host keys, no rate limiting |
| Testing | 25% coverage (4,689/18,424 lines) | No CI/CD, critical paths untested |
| Alert Adapters | 5 adapters, 100% tested | Missing Opsgenie/New Relic, incomplete validation |

---

## 🔴 Critical Priority (Security & Stability)

### 1. Fix SSH Host Key Verification
**Location:** `mcp-gateway/internal/tools/ssh/ssh.go` (lines 309, 351, 390)
**Issue:** Currently uses `ssh.InsecureIgnoreHostKey()` - vulnerable to MITM attacks
**Fix:** Implement proper known_hosts file verification or certificate pinning

### 2. Add CI/CD Pipeline
**Issue:** No automated testing on PR - breaking changes can be merged
**Fix:** Set up GitHub Actions with:
- Run tests on every PR
- Coverage reporting with minimum threshold (70%)
- Block merging if tests fail

### 3. Complete PagerDuty HMAC Validation
**Location:** `internal/alerts/adapters/pagerduty.go`
**Issue:** Only checks `v1=` prefix format, doesn't verify actual HMAC-SHA256 signature
**Fix:** Implement full cryptographic signature verification

### 4. Add Handler & Service Tests
**Current:** 0% test coverage on 5,346 lines of critical code
**Files needing tests:**
- `internal/handlers/api.go` (1,486 lines)
- `internal/handlers/alert.go`
- `internal/services/skill_service.go`
- `internal/services/tool_service.go`
- `codex-worker/internal/runner.go`

---

## 🟠 High Priority (Reliability & Usability)

### 5. Standardize API Error Handling
**Issue:** 120 `http.Error()` calls with inconsistent formats, error info leakage
**Fix:**
- Create custom error types (`InvalidInput`, `NotFound`, `Conflict`)
- Unified response format: `{"error": "...", "code": "...", "request_id": "..."}`
- Request ID middleware for tracing
- Panic recovery middleware

### 6. Add Request Rate Limiting
**Issue:** No protection against abuse - can exhaust resources
**Locations:**
- MCP Gateway tool calls
- API endpoints
- Webhook endpoints
**Fix:** Add per-endpoint and per-client rate limiting middleware

### 7. Create Frontend Data Fetching Hook
**Issue:** Every page duplicates loading/error/success state patterns (~50 lines each)
**Fix:** Create `useAsyncData()` hook or adopt React Query/SWR
```typescript
// Replace manual state with:
const { data, loading, error, refetch } = useAsyncData(() => api.list());
```

### 8. Add Input Validation Middleware
**Issue:** No validation of request bodies or query params
**Fix:**
- JSON schema validation for API requests
- Add size limits
- Sanitize inputs at middleware level

### 9. Add Tool Credential Caching
**Location:** `mcp-gateway/internal/database/db.go`
**Issue:** Every tool call fetches credentials from database
**Fix:** Add TTL-based credential cache (5-15 min) with invalidation

### 10. Fix CORS Configuration
**Location:** `internal/middleware/cors.go`
**Issue:** Allows all origins by default - CSRF vulnerability
**Fix:** Configure explicit allowed origins for production

---

## 🟡 Medium Priority (Performance & Developer Experience)

### 11. Add API Documentation (OpenAPI/Swagger)
**Issue:** Only basic examples in README, no formal API spec
**Fix:** Generate OpenAPI 3.0 spec with:
- All endpoints documented
- Request/response schemas
- Error codes and meanings
- Authentication flows

### 12. Implement Pagination
**Issue:** Incidents table loads all records at once
**Locations:**
- `GET /api/incidents`
- `GET /api/skills`
- `GET /api/tools`
**Fix:** Add `page` & `limit` query params with total count in response

### 13. Add Search & Filtering
**Issue:** Can't find incidents by ID/hostname/status
**Fix:** Add search input + query params for filtering:
- `/api/incidents?status=running&host=web-1&q=disk`

### 14. Implement Context Propagation in SSH
**Location:** `mcp-gateway/internal/tools/ssh/ssh.go` (lines 274, 320)
**Issue:** Methods accept `ctx context.Context` but don't use it for cancellation
**Fix:** Use `net.Dialer` with context to make SSH connections cancellable

### 15. Add Audit Logging
**Issue:** Tool calls not logged with who/when/what for incident investigation
**Fix:** Database audit trail table with:
- User/API key that made the request
- Tool called, arguments passed
- Success/failure status
- Duration

### 16. Refactor Large Handler Files
**Issue:** `api.go` is 1,486 lines - too large to maintain
**Fix:** Split by resource:
- `handlers/skills.go`
- `handlers/tools.go`
- `handlers/incidents.go`
- `handlers/settings.go`
- `handlers/context.go`

### 17. Add Frontend Component Tests
**Issue:** No React component tests
**Fix:** Set up Vitest + React Testing Library for:
- Critical user flows (login, incident view)
- Form validation
- Modal interactions

### 18. Fix Unbounded Goroutines
**Location:** `internal/handlers/alert.go` line 135
**Issue:** Alert processing spawns unlimited goroutines - resource exhaustion risk
```go
go h.processAlert(instance, normalizedAlert)  // Unbounded
```
**Fix:** Use worker pool pattern or `sync.Semaphore`

---

## 🟢 Lower Priority (Enhancements)

### 19. Add Missing Alert Adapters
**Priority order:**
1. **Opsgenie** - Incident management (similar to PagerDuty)
2. **New Relic** - APM/monitoring (growing adoption)
3. **Splunk** - Enterprise monitoring
4. **Dynatrace** - Application monitoring

### 20. Add WebSocket for Real-time Updates
**Issue:** Pages poll with setInterval instead of subscribing
**Fix:** WebSocket connection for:
- Incident status changes
- Log streaming
- Alert notifications

### 21. Add Keyboard Shortcuts
**Issue:** Power users can't navigate efficiently
**Fix:** Command palette (Cmd+K) with:
- Quick navigation to incidents/skills/tools
- Search across all entities
- Common actions

### 22. Implement Export Functionality
**Issue:** Can't export incident logs for analysis
**Fix:** Add "Export as JSON/CSV" buttons for:
- Incident logs
- Alert history
- Tool execution results

### 23. Add Frontend Virtualization
**Issue:** Large incident lists could cause lag
**Fix:** Implement `react-virtual` for tables with 100+ rows

### 24. Improve Accessibility
**Issues found:**
- No ARIA labels on interactive elements
- Focus not trapped in modals
- No skip-to-content link
**Fix:** WCAG 2.1 AA compliance audit

### 25. Enhance Field Mapping
**Current limitations:**
- No array indexing in dot-notation
- No regex transformations
- No fallback chains
**Fix:** Extended mapping syntax:
```json
{
  "host": "alerts[0].labels.instance || annotations.host || 'unknown'"
}
```

---

## Implementation Phases

### Phase 1: Security & Testing Foundation (2-3 weeks)
- [ ] Fix SSH host key verification (#1)
- [ ] Set up CI/CD pipeline (#2)
- [ ] Complete PagerDuty HMAC validation (#3)
- [ ] Add handler tests for critical endpoints (#4)

### Phase 2: Error Handling & Reliability (2 weeks)
- [ ] Standardize API error handling (#5)
- [ ] Add request rate limiting (#6)
- [ ] Add input validation middleware (#8)
- [ ] Fix unbounded goroutines (#18)

### Phase 3: Developer Experience (2 weeks)
- [ ] Create `useAsyncData()` hook (#7)
- [ ] Add OpenAPI documentation (#11)
- [ ] Refactor large handler files (#16)
- [ ] Add frontend component tests (#17)

### Phase 4: Features & Performance (2-3 weeks)
- [ ] Implement pagination (#12)
- [ ] Add search & filtering (#13)
- [ ] Add credential caching (#9)
- [ ] Add audit logging (#15)

### Phase 5: Enhancements (ongoing)
- [ ] Add Opsgenie adapter (#19)
- [ ] WebSocket for real-time updates (#20)
- [ ] Export functionality (#22)
- [ ] Accessibility improvements (#24)

---

## Quick Wins (< 1 hour each)

1. **Enable coverage reporting** - `make test-coverage` already exists
2. **Add pre-commit hook** - Run `make verify` before commits
3. **Fix CORS origins** - Add explicit allowed origins in config
4. **Add request ID header** - Simple middleware addition
5. **Extract API constants** - Create `constants.go` for magic strings

---

## Metrics & Success Criteria

| Metric | Current | Target |
|--------|---------|--------|
| Test coverage | 25% | 70%+ |
| Handler test coverage | 0% | 80%+ |
| Service test coverage | 0% | 80%+ |
| API documentation | Partial | 100% OpenAPI |
| Security vulnerabilities | 3 critical | 0 critical |
| Response time p99 | Unknown | < 500ms |

---

## Files Referenced

**Critical paths needing tests:**
- `/home/user/akmatori/internal/handlers/api.go`
- `/home/user/akmatori/internal/handlers/alert.go`
- `/home/user/akmatori/internal/services/skill_service.go`
- `/home/user/akmatori/internal/services/tool_service.go`
- `/home/user/akmatori/codex-worker/internal/runner.go`

**Security fixes needed:**
- `/home/user/akmatori/mcp-gateway/internal/tools/ssh/ssh.go`
- `/home/user/akmatori/internal/alerts/adapters/pagerduty.go`
- `/home/user/akmatori/internal/middleware/cors.go`

**Frontend improvements:**
- `/home/user/akmatori/web/src/pages/Incidents.tsx` (617 lines - split)
- `/home/user/akmatori/web/src/api/client.ts` (346 lines - add caching)
- Create: `/home/user/akmatori/web/src/hooks/useAsyncData.ts`
