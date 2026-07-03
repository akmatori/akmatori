# Test Helpers

Reusable test building blocks live in `internal/testhelpers`.

## Preferred patterns

- Use builders for readable fixture setup instead of hand-populating large structs.
- Use `NewHTTPTestContext` for handler tests so request/response assertions stay compact.
- Use service mocks when the behavior under test is handler/service orchestration, not storage.
- Use fixtures from `tests/fixtures/` for real payload samples; `LoadFixture` caches file contents and returns a fresh copy per call so tests can mutate payload bytes safely.
- Keep fixture paths relative to `tests/fixtures/`; helpers intentionally reject absolute paths and `../` traversal.
- Add benchmarks only for helpers or hot paths that are easy to compare over time.

## Common helpers

**Builders**
- `NewSkillBuilder`
- `NewToolInstanceBuilder`
- `NewToolTypeBuilder`
- `NewAlertSourceInstanceBuilder`
- `NewRunbookBuilder`
- `NewContextFileBuilder`
- `NewAlertBuilder`
- `NewIncidentBuilder`

**HTTP tests**
```go
ctx := NewHTTPTestContext(t, http.MethodPost, "/api/alerts", nil).
    WithAPIKey("test-key").
    WithJSONBody(payload).
    Execute(handler).
    AssertStatus(http.StatusCreated).
    AssertJSONContentType().
    AssertJSONBody(`{"ok":true}`).
    AssertJSONField("result.uuid", "alert-uuid").
    AssertJSONField("items.0.name", "first item")

ctx.AssertJSONError(http.StatusConflict, "name already exists", "duplicate_name")
```

**Fixtures**
```go
data := LoadFixture(t, "alerts/alertmanager_firing.json")
LoadJSONFixture(t, "alerts/alertmanager_firing.json", &payload)
```

**SQLite test DBs**
```go
db := NewSQLiteDB(t, &database.HTTPConnector{})
db := NewGlobalSQLiteDB(t, &database.SystemSetting{})
```

**Mocks**
```go
alertSvc := NewMockAlertService().
    WithInstance("uuid", &instance).
    WithProcessedAlerts(alert)
```

**Async assertions**
```go
AssertEventually(t, 5*time.Second, 100*time.Millisecond, func() bool {
    return ready()
}, "service should become ready")
```
