# Test Helpers

Reusable test building blocks live in `internal/testhelpers`.

## Preferred patterns

- Use builders for readable fixture setup instead of hand-populating large structs.
- Use `NewHTTPTestContext` for handler tests so request/response assertions stay compact.
- Use service mocks when the behavior under test is handler/service orchestration, not storage.
- Use fixtures from `tests/fixtures/` for real payload samples.
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
    AssertJSONBody(`{"ok":true}`)
```

**Fixtures**
```go
data := LoadFixture(t, "alerts/alertmanager_firing.json")
LoadJSONFixture(t, "alerts/alertmanager_firing.json", &payload)
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
