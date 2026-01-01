# Contributing to Akmatori

This guide covers how to contribute to Akmatori, including adding new tools and maintaining existing functionality.

## Project Structure

```
akmatori/
├── cmd/akmatori/          # Main application entry point
├── internal/
│   ├── database/          # Database models and migrations
│   ├── executor/          # Codex CLI executor (runs AI agents)
│   ├── handlers/          # HTTP API handlers
│   ├── services/          # Business logic services
│   └── utils/             # Utility functions
├── tools/                 # Python tool implementations
│   ├── ssh/               # SSH remote execution tool
│   └── zabbix/            # Zabbix monitoring tool
├── web/                   # React frontend
├── docs/                  # Documentation
└── akmatori_data/         # Runtime data (skills, incidents, context)
```

## Adding a New Tool

### Step 1: Create Tool Directory

Create a new directory under `tools/`:

```
tools/your_tool/
├── __init__.py           # Export public functions
├── config.py             # Configuration loader (auto-loads from .env)
├── your_tool.py          # Main implementation
└── tool_metadata.json    # Tool type definition
```

### Step 2: Define Tool Metadata

Create `tool_metadata.json`:

```json
{
  "name": "your_tool",
  "display_name": "Your Tool Name",
  "description": "Brief description of what your tool does.",
  "category": "monitoring",
  "settings": {
    "your_tool_url": {
      "type": "string",
      "label": "API URL",
      "description": "URL of the API endpoint",
      "required": true,
      "placeholder": "https://api.example.com"
    },
    "your_tool_api_key": {
      "type": "password",
      "label": "API Key",
      "description": "API authentication key",
      "required": true
    }
  }
}
```

### Step 3: Implement Config Loader

Create `config.py` that auto-loads from `.env.your_tool`:

```python
import os
import base64

_config = {}

def _load_config_env():
    """Load configuration from environment variables."""
    global _config

    url = os.environ.get('YOUR_TOOL_URL', '')
    api_key = os.environ.get('YOUR_TOOL_API_KEY', '')

    # Handle base64-encoded values
    if api_key.startswith('base64:'):
        api_key = base64.b64decode(api_key[7:]).decode('utf-8')

    _config = {
        'url': url,
        'api_key': api_key
    }

def get_config():
    """Get the current configuration."""
    if not _config:
        _load_config_env()
    return _config

# Auto-load on import
_load_config_env()
```

### Step 4: Implement Tool Functions

Create your main tool file with public functions:

```python
from .config import get_config
import requests
import json

def get_items():
    """Get all items from the API.

    Returns:
        JSON string with the list of items
    """
    config = get_config()
    response = requests.get(
        f"{config['url']}/items",
        headers={'Authorization': f"Bearer {config['api_key']}"}
    )
    return json.dumps(response.json(), indent=2)

def get_item(item_id: str):
    """Get a specific item by ID.

    Args:
        item_id: The item identifier

    Returns:
        JSON string with item details
    """
    config = get_config()
    response = requests.get(
        f"{config['url']}/items/{item_id}",
        headers={'Authorization': f"Bearer {config['api_key']}"}
    )
    return json.dumps(response.json(), indent=2)
```

### Step 5: Export Functions

In `__init__.py`:

```python
from .your_tool import get_items, get_item
```

### Step 6: Add Quick Start Examples

Update `generateToolsDocumentation()` in `internal/services/skill_service.go`:

```go
case "your_tool":
    sb.WriteString("print(get_items())\n")
    sb.WriteString("print(get_item('item-123'))\n")
    sb.WriteString("```\n\n")
```

### Step 7: Test

1. Restart the container: `docker-compose restart akmatori`
2. Create a new tool instance in the UI
3. Assign it to a skill
4. Verify tools.md is generated with correct Quick Start code
5. Test the Quick Start code in an incident

## Key Code Locations

### Tool Documentation Generation

| File | Function | Purpose |
|------|----------|---------|
| `skill_service.go` | `generateToolsDocumentation()` | Generates tools.md with Quick Start |
| `skill_service.go` | `generateIncidentAgentsMd()` | Generates AGENTS.md for incidents |
| `skill_service.go` | `generateSkillMd()` | Generates SKILL.md for skills |
| `skill_service.go` | `RegenerateAllSkillMds()` | Regenerates all on startup |

### Tool Loading

| File | Function | Purpose |
|------|----------|---------|
| `tool_service.go` | `LoadToolTypes()` | Loads tool_metadata.json from tools/ |
| `tool_service.go` | `CopyToolToSkillLib()` | Symlinks tool Python files to skill |
| `tool_service.go` | `GenerateToolDescription()` | Introspects Python for function docs |

### Incident Execution

| File | Function | Purpose |
|------|----------|---------|
| `codex.go` | `Run()` | Executes Codex CLI for incident |
| `codex.go` | `processJSONEvents()` | Parses agent reasoning/commands |
| `skill_service.go` | `SpawnIncidentManager()` | Sets up incident environment |
| `skill_service.go` | `generateIncidentEnvFiles()` | Creates .env files for tools |

## Documentation Architecture

See [TOOL_ARCHITECTURE.md](./TOOL_ARCHITECTURE.md) for detailed documentation on:
- How AGENTS.md, SKILL.md, and tools.md work together
- The "Single Source of Truth" principle
- Why tools.md has the authoritative warning box

## Testing Changes

### Backend

```bash
# Build
go build -o akmatori ./cmd/akmatori

# Rebuild Docker image
docker-compose build akmatori

# Restart with new image
docker-compose up -d akmatori

# Check logs
docker-compose logs -f akmatori
```

### Frontend

```bash
cd web
npm install
npm run build
docker-compose build frontend
docker-compose up -d frontend
```

## Code Style

### Go

- Use `gofmt` for formatting
- Export only what's needed from packages
- Add comments for exported functions
- Use meaningful variable names

### Python (Tools)

- Use docstrings for all public functions
- Return JSON strings (not dicts) for agent consumption
- Handle base64-encoded secrets in config loaders
- Auto-load config on module import

## Common Pitfalls

### 1. Tool Not Appearing in UI

- Check `tool_metadata.json` is valid JSON
- Restart container to reload tool types
- Check logs for parsing errors

### 2. Quick Start Code Doesn't Work

- Ensure function names match `__init__.py` exports
- Test the exact code you put in `generateToolsDocumentation()`
- Check .env file is being generated with correct keys

### 3. Agent Ignores Documentation

- Ensure tools.md has the warning box at the top
- Verify the symlink from incident's .codex/skills/ to /akmatori/skills/
- Check AGENTS.md has the "DO NOT read source files" instruction

### 4. Environment Variables Wrong

- Keys should match tool_metadata.json settings (e.g., `ssh_servers` -> `SSH_SERVERS`)
- Multi-line values (like SSH keys) should be base64-encoded
- Config loaders must handle both prefixed and non-prefixed names
