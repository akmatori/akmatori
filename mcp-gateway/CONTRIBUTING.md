# Adding New Tools to MCP Gateway

This guide explains how to add new tools to the MCP Gateway. Tools are Go implementations that:
1. Fetch credentials from the database
2. Execute external API calls or operations
3. Return results to Codex via MCP protocol

## Architecture Overview

```
┌─────────────────────┐     ┌─────────────────────┐     ┌─────────────────────┐
│   Codex Container   │     │    MCP Gateway      │     │     Database        │
│                     │     │                     │     │                     │
│   Python Wrapper    │────>│   Tool Handler      │────>│  Tool Credentials   │
│   (thin MCP call)   │ MCP │   (Go impl)         │     │  (encrypted)        │
│                     │<────│                     │────>│                     │
│                     │     │                     │     │  External Service   │
└─────────────────────┘     └─────────────────────┘     └─────────────────────┘
```

**Key Security Feature**: Codex never sees credentials. The MCP Gateway fetches them from the database at execution time.

## Step 1: Create Tool Package

Create a new package in `internal/tools/{tool_name}/`:

```go
// internal/tools/datadog/datadog.go
package datadog

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/akmatori/mcp-gateway/internal/database"
)

// DatadogTool implements Datadog API operations
type DatadogTool struct {
    db *database.DB
}

// New creates a new Datadog tool
func New(db *database.DB) *DatadogTool {
    return &DatadogTool{db: db}
}

// GetMetrics fetches metrics from Datadog
func (t *DatadogTool) GetMetrics(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
    // 1. Fetch credentials from database
    creds, err := t.db.GetToolCredentialsForIncident(ctx, incidentID, "datadog")
    if err != nil {
        return "", fmt.Errorf("failed to get Datadog credentials: %w", err)
    }

    // 2. Extract settings
    apiKey := creds.Settings["api_key"].(string)
    appKey := creds.Settings["app_key"].(string)
    site := creds.Settings["site"].(string) // e.g., "datadoghq.com"

    // 3. Extract arguments
    query := args["query"].(string)
    from := int64(args["from"].(float64))
    to := int64(args["to"].(float64))

    // 4. Make API request
    url := fmt.Sprintf("https://api.%s/api/v1/query?query=%s&from=%d&to=%d",
        site, query, from, to)

    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return "", err
    }
    req.Header.Set("DD-API-KEY", apiKey)
    req.Header.Set("DD-APPLICATION-KEY", appKey)

    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    // 5. Return result as JSON string
    var result map[string]interface{}
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", err
    }

    jsonBytes, err := json.MarshalIndent(result, "", "  ")
    if err != nil {
        return "", err
    }
    return string(jsonBytes), nil
}
```

## Step 2: Register Tool in Registry

Update `internal/tools/registry.go`:

```go
package tools

import (
    "github.com/akmatori/mcp-gateway/internal/database"
    "github.com/akmatori/mcp-gateway/internal/mcp"
    "github.com/akmatori/mcp-gateway/internal/tools/datadog"
    "github.com/akmatori/mcp-gateway/internal/tools/ssh"
    "github.com/akmatori/mcp-gateway/internal/tools/zabbix"
)

// RegisterTools registers all available tools with the MCP server
func RegisterTools(server *mcp.Server, db *database.DB) {
    // Existing tools
    sshTool := ssh.New(db)
    zabbixTool := zabbix.New(db)

    // New tool
    datadogTool := datadog.New(db)

    // Register Datadog tools
    server.RegisterTool(mcp.Tool{
        Name:        "datadog.get_metrics",
        Description: "Query metrics from Datadog",
        InputSchema: mcp.InputSchema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "query": {
                    Type:        "string",
                    Description: "Datadog query string",
                },
                "from": {
                    Type:        "integer",
                    Description: "Start timestamp (Unix epoch)",
                },
                "to": {
                    Type:        "integer",
                    Description: "End timestamp (Unix epoch)",
                },
            },
            Required: []string{"query", "from", "to"},
        },
    }, func(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
        return datadogTool.GetMetrics(ctx, incidentID, args)
    })
}
```

## Step 3: Create Python Wrapper

Add a thin wrapper in `codex-tools/tools/{tool_name}.py`:

```python
# codex-tools/tools/datadog.py
"""Datadog monitoring tool - thin wrapper over MCP Gateway.

This module provides access to Datadog metrics and events.
All credentials are securely managed by MCP Gateway.
"""

from mcp_client import MCPClient
from typing import Optional, Dict, Any, List

_client = MCPClient()


def get_metrics(
    query: str,
    from_ts: int,
    to_ts: int
) -> Dict[str, Any]:
    """Query metrics from Datadog.

    Args:
        query: Datadog metric query string (e.g., "avg:system.cpu.user{*}")
        from_ts: Start timestamp (Unix epoch seconds)
        to_ts: End timestamp (Unix epoch seconds)

    Returns:
        Dictionary with query results containing series data

    Example:
        >>> import time
        >>> now = int(time.time())
        >>> metrics = get_metrics("avg:system.cpu.user{*}", now - 3600, now)
        >>> for series in metrics.get("series", []):
        ...     print(f"{series['metric']}: {series['pointlist'][-1][1]}%")
    """
    return _client.call("datadog.get_metrics", {
        "query": query,
        "from": from_ts,
        "to": to_ts
    })


def get_events(
    start: int,
    end: int,
    priority: str = "normal",
    tags: Optional[List[str]] = None
) -> List[Dict[str, Any]]:
    """Get events from Datadog.

    Args:
        start: Start timestamp (Unix epoch seconds)
        end: End timestamp (Unix epoch seconds)
        priority: Event priority filter ("normal" or "low")
        tags: Optional list of tags to filter events

    Returns:
        List of event dictionaries

    Example:
        >>> import time
        >>> now = int(time.time())
        >>> events = get_events(now - 86400, now, tags=["env:production"])
        >>> for event in events:
        ...     print(f"{event['title']}: {event['text'][:50]}...")
    """
    return _client.call("datadog.get_events", {
        "start": start,
        "end": end,
        "priority": priority,
        "tags": tags or []
    })
```

## Step 4: Add Tool Type to Database

Create the tool type via the API or directly in the database:

```sql
INSERT INTO tool_types (name, description, settings_schema, created_at, updated_at)
VALUES (
    'datadog',
    'Datadog monitoring and metrics platform',
    '{
        "type": "object",
        "properties": {
            "api_key": {"type": "string", "description": "Datadog API key"},
            "app_key": {"type": "string", "description": "Datadog Application key"},
            "site": {"type": "string", "description": "Datadog site (e.g., datadoghq.com)"}
        },
        "required": ["api_key", "app_key", "site"]
    }',
    NOW(),
    NOW()
);
```

Or via the Web UI: Settings > Tools > Add Tool Type

## Best Practices

### Error Handling

```go
// Always wrap errors with context
if err != nil {
    return "", fmt.Errorf("failed to query Datadog metrics: %w", err)
}

// Use context timeout
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
```

### Credential Security

```go
// NEVER log credentials
log.Printf("Querying Datadog for incident %s", incidentID)  // OK
log.Printf("API Key: %s", apiKey)  // NEVER DO THIS

// NEVER return credentials in responses
result := map[string]interface{}{
    "data": metrics,
    // "api_key": apiKey,  // NEVER include credentials
}
```

### Input Validation

```go
// Validate required arguments
query, ok := args["query"].(string)
if !ok || query == "" {
    return "", fmt.Errorf("query is required")
}

// Validate numeric arguments
from, ok := args["from"].(float64)
if !ok {
    return "", fmt.Errorf("from must be a number")
}
```

### Parallel Operations

For tools that operate on multiple targets (like SSH):

```go
func (t *MyTool) ExecuteOnMultiple(ctx context.Context, targets []string, command string) ([]Result, error) {
    results := make([]Result, len(targets))
    var wg sync.WaitGroup

    for i, target := range targets {
        wg.Add(1)
        go func(idx int, t string) {
            defer wg.Done()
            results[idx] = t.ExecuteOnSingle(ctx, t, command)
        }(i, target)
    }

    wg.Wait()
    return results, nil
}
```

## Testing Your Tool

1. **Unit Tests**: Test individual functions with mocked credentials
2. **Integration Tests**: Test against real services with test credentials
3. **MCP Protocol Tests**: Test the tool via MCP calls

```go
// internal/tools/datadog/datadog_test.go
func TestGetMetrics(t *testing.T) {
    // Mock database
    mockDB := &MockDB{
        Credentials: map[string]interface{}{
            "api_key": "test-key",
            "app_key": "test-app-key",
            "site":    "datadoghq.com",
        },
    }

    tool := New(mockDB)

    // Test with mock HTTP server
    // ...
}
```

## Updating Existing Tools

When modifying existing tools:

1. Maintain backward compatibility with existing wrappers
2. Add new functions rather than changing existing signatures
3. Update the Python wrapper documentation
4. Test with existing incidents to ensure no regression

## Security Checklist

Before submitting a new tool:

- [ ] Credentials are fetched from database, never hardcoded
- [ ] No credentials in log messages
- [ ] No credentials in error messages or responses
- [ ] Context timeout set for external calls
- [ ] Input validation for all user-provided arguments
- [ ] Proper error handling with wrapped errors
- [ ] Python wrapper has complete docstrings
