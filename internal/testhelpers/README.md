# Test Helpers Package

This package provides reusable testing utilities for Akmatori.

## Builders

Fluent builders for creating test data:

```go
// Create a skill with tools
skill := NewSkillBuilder().
    WithName("zabbix-analyst").
    WithCategory("monitoring").
    AsSystem().
    Build()

// Create an alert source instance
source := NewAlertSourceInstanceBuilder().
    WithName("Production Alertmanager").
    WithWebhookSecret("secret").
    Build()

// Create a runbook
runbook := NewRunbookBuilder().
    WithTitle("Database Failover").
    WithContent("# Steps...").
    Build()

// Create a context file
file := NewContextFileBuilder().
    WithFilename("architecture.md").
    AsMarkdown().
    Build()
```

Available builders:
- `SkillBuilder` - Skills with tools
- `ToolInstanceBuilder` - Tool instances
- `ToolTypeBuilder` - Tool type definitions
- `AlertSourceInstanceBuilder` - Alert source instances
- `LLMSettingsBuilder` - LLM provider settings
- `SlackSettingsBuilder` - Slack integration settings
- `RunbookBuilder` - Runbook documents
- `ContextFileBuilder` - Context files
- `NormalizedAlertBuilder` - Normalized alerts
- `IncidentBuilder` - Incidents

## HTTP Testing

```go
// Fluent HTTP test context
NewHTTPTestContext(t, "POST", "/api/alerts", nil).
    WithJSONBody(alertPayload).
    WithAPIKey("test-key").
    Execute(handler).
    AssertStatus(http.StatusOK).
    AssertBodyContains("created")
```

## Assertions

```go
// JSON assertions
AssertJSONEqual(t, expected, actual, "response body")
AssertJSONContainsKey(t, json, "id", "should have id")
AssertJSONArrayLength(t, json, 3, "should have 3 items")

// Slice/map assertions
AssertSliceContains(t, items, target, "should contain item")
AssertMapContainsKey(t, m, "key", "should have key")

// Time assertions
AssertTimeWithin(t, actual, expected, time.Second, "within 1s")

// Error assertions
AssertErrorContains(t, err, "not found", "should mention not found")
AssertPanics(t, func() { ... }, "should panic")
```

## Concurrent Testing

```go
// Run function concurrently
ConcurrentTest(t, 10, func(workerID int) {
    // Each worker runs this
})

// With timeout
ConcurrentTestWithTimeout(t, 5*time.Second, 10, func(workerID int) {
    // Must complete within 5s
})
```

## Environment Helpers

```go
// Temporarily set env var
cleanup := WithEnv(t, "DATABASE_URL", "test-db")
defer cleanup()

// Multiple env vars
cleanup := WithEnvs(t, map[string]string{
    "API_KEY": "test",
    "DEBUG":   "true",
})
defer cleanup()
```

## File Helpers

```go
// Create temp directory
dir, cleanup := TempTestDir(t, "test-")
defer cleanup()

// Write test file
path := WriteTestFile(t, dir, "config.json", `{"key": "value"}`)

// Assert file state
AssertFileExists(t, path, "config should exist")
AssertFileContains(t, path, "key", "should contain key")
```

## Fixtures

Load test fixtures from `tests/fixtures/`:

```go
data := LoadFixture(t, "alerts/alertmanager_firing.json")
```

Available fixtures:
- `alerts/alertmanager_firing.json`
- `alerts/datadog_monitor.json`
- `alerts/grafana_alerting.json`
- `alerts/pagerduty_trigger.json`
- `alerts/zabbix_problem.json`
- `runbooks/database_failover.md`

## Mock Adapter

```go
adapter := NewMockAlertAdapter("test").
    WithAlerts(alert1, alert2).
    WithParseError(nil)

alerts, err := adapter.ParsePayload(body, instance)
```

## Call Counter

Thread-safe counter for tracking function calls:

```go
counter := NewCallCounter()
handler := func() { counter.Inc() }

// Use handler...

counter.AssertCount(t, 5, "should be called 5 times")
```

## Eventually/Retry

For async operations:

```go
AssertEventually(t, 5*time.Second, 100*time.Millisecond, func() bool {
    return service.IsReady()
}, "service should become ready")
```
