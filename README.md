# Akmatori

Akmatori is an AI-powered AIOps agent that integrates with monitoring systems and Slack to provide intelligent incident response and automated remediation.

<img width="1436" height="659" alt="image" src="https://github.com/user-attachments/assets/b2c78bf5-9e20-47da-8ec6-b841c6a0a3de" />


## Features

- **Multi-Source Alert Ingestion**: Receive alerts from Alertmanager, Zabbix, PagerDuty, Grafana, and Datadog via webhooks
- **Slack Integration**: Post incidents to channels, receive commands, and provide real-time updates
- **LLM-Powered Automation**: Use OpenAI's models via [Codex CLI](https://github.com/openai/codex) to analyze incidents and execute remediation skills
- **[Agent Skills](https://github.com/agentskills/agentskills) Format**: Skills follow the open Agent Skills specification for portability across AI agents
- **Tools Management**: Configure reusable tools (Python scripts, API clients) for skills
- **Web Dashboard**: Manage incidents, skills, tools, and settings through a modern UI
- **Context Files**: Upload reference documents for the AI to use during incident analysis

## Architecture

Akmatori uses a secure 4-container architecture with network isolation:

```
                         ┌──────────────────────────────────────────────┐
                         │              Alert Sources                    │
                         │  Alertmanager │ Zabbix │ PagerDuty │ Grafana  │
                         └───────────────────────┬──────────────────────┘
                                                 │
                                                 ▼
┌─────────────┐         ┌────────────────────────────────────────────────────────┐
│    Slack    │◀───────▶│                    API Container                       │
│     Bot     │         │  • Incident management    • Skill orchestration        │
└─────────────┘         │  • Alert ingestion        • WebSocket to Codex Worker  │
                        └──────────┬──────────────────────────┬──────────────────┘
                                   │                          │
                    ┌──────────────┴──────────────┐           │ WebSocket
                    │         PostgreSQL          │           │
                    │  • Incidents, Skills, Tools │           │
                    │  • Credentials (encrypted)  │           │
                    └──────────────┬──────────────┘           │
                                   │                          │
                    ┌──────────────┴──────────────┐           │
                    │        MCP Gateway          │◀──────────┼───────────────┐
                    │  • Fetches credentials      │           │               │
                    │  • SSH/Zabbix execution     │           │               │
                    └──────────────┬──────────────┘           │               │
                                   │                          │               │
                                   │              ┌───────────▼───────────┐   │
                                   │              │    Codex Worker       │   │
                                   │              │  • Runs Codex CLI     │───┘
                                   └─────────────▶│  • NO database access │  MCP calls
                                    Tool calls    │  • NO direct secrets  │
                                                  └───────────┬───────────┘
                                                              │
                                                  ┌───────────▼───────────┐
                                                  │       OpenAI API      │
                                                  └───────────────────────┘
```

### Security Design

| Container | Database Access | Secrets Access | External Network |
|-----------|----------------|----------------|------------------|
| API | ✅ Full | ✅ All | ✅ Slack |
| MCP Gateway | ✅ Read-only | ✅ Tool credentials | ✅ SSH, APIs |
| Codex Worker | ❌ None | ❌ None (passed per-incident) | ✅ OpenAI only |
| PostgreSQL | N/A | N/A | ❌ Internal only |

**Key security features:**
- **Credential isolation**: Codex container never sees database credentials
- **Per-incident auth**: OpenAI API key passed via WebSocket for each task
- **Network segmentation**: Three isolated Docker networks
- **UID separation**: API (UID 1000) and Codex (UID 1001) for file permission control

## Quick Start

### Prerequisites

- Docker and Docker Compose
- OpenAI API key
- Slack App (optional, for Slack integration)

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/akmatori/akmatori.git
   cd akmatori
   ```

2. Create environment file:
   ```bash
   cp .env.example .env
   ```

3. Edit `.env` with your configuration:
   ```bash
   ADMIN_PASSWORD=your-secure-password
   HTTP_PORT=8080
   POSTGRES_USER=akmatori
   POSTGRES_PASSWORD=your-db-password
   POSTGRES_DB=akmatori
   ```

4. Start the services:
   ```bash
   docker-compose up -d
   ```

5. Access the web dashboard at `http://localhost:8080`

## Configuration

### OpenAI Setup

1. Navigate to **Settings > OpenAI** in the web dashboard
2. Enter your OpenAI API key
3. Select the model and reasoning effort level

### Slack Setup

1. Create a Slack App at https://api.slack.com/apps
2. Enable Socket Mode and generate an App Token
3. Add Bot Token Scopes: `chat:write`, `channels:history`, `app_mentions:read`
4. Install the app to your workspace
5. Navigate to **Settings > Slack** in the dashboard and enter:
   - Bot Token (`xoxb-...`)
   - App Token (`xapp-...`)
   - Signing Secret
   - Alerts Channel

### Alert Sources Setup

Akmatori can receive alerts from multiple monitoring systems. Each alert source instance gets a unique webhook URL.

1. Navigate to **Settings > Alert Sources** in the dashboard
2. Click **New Source**
3. Select the source type:
   - **Alertmanager** - Prometheus Alertmanager
   - **Zabbix** - Zabbix monitoring
   - **PagerDuty** - PagerDuty incident management
   - **Grafana** - Grafana alerting
   - **Datadog** - Datadog monitoring
4. Provide an instance name and optional webhook secret
5. Copy the generated webhook URL and configure it in your monitoring system

Each incoming alert automatically creates an incident and triggers AI-powered investigation.

## Skills

Skills are AI-powered automation units that can be triggered to handle specific tasks. Akmatori implements the [Agent Skills](https://github.com/agentskills/agentskills) open format, making skills portable and reusable across different AI agents.

### Agent Skills Format

Each skill is stored as a directory with a `SKILL.md` file following the [Agent Skills specification](https://agentskills.io/specification):

```
skills/
└── disk-cleanup/
    ├── SKILL.md          # Skill definition (YAML frontmatter + instructions)
    ├── scripts/          # Optional executable scripts
    └── references/       # Optional additional documentation
```

**SKILL.md structure:**
```markdown
---
name: disk-cleanup
description: Analyzes disk usage and cleans up temporary files
---

You are a disk cleanup specialist. When triggered:

1. Check disk usage on the target host
2. Identify large files and directories
3. Suggest safe cleanup actions
4. Execute cleanup with user approval
```

### Creating a Skill

**Via Dashboard:**
1. Navigate to **Skills** in the dashboard
2. Click **Create Skill**
3. Provide:
   - **Name**: Unique identifier (e.g., `disk-cleanup`)
   - **Description**: What the skill does
   - **Category**: Grouping (e.g., `infrastructure`, `database`)
   - **Prompt**: Instructions for the AI

**Via Filesystem:**
1. Create a directory in `/akmatori/skills/{skill-name}/`
2. Add a `SKILL.md` file with YAML frontmatter and instructions
3. Run skill sync via API or restart the service

### Attaching Tools

Tools provide skills with capabilities like API access or script execution:

1. Navigate to **Tools** to create tool instances
2. Configure tool settings (API endpoints, credentials)
3. Attach tools to skills via the skill edit page

Tools are symlinked into the skill's directory, making them available during execution.

## Tools

### Built-in Tool Types

- **SSH**: Execute commands on remote servers via SSH (parallel execution, connectivity testing)
- **Zabbix**: Interact with Zabbix API (get hosts, problems, triggers)

### Creating Custom Tools

Place Python scripts in the `tools/` directory following this structure:
```
tools/
└── your-tool/
    ├── README.md
    ├── requirements.txt
    └── your_script.py
```

## API

### Incidents

```bash
# Create an incident
curl -X POST http://localhost:8080/api/incidents \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"task": "Check disk usage on server01"}'

# List incidents
curl http://localhost:8080/api/incidents \
  -H "Authorization: Bearer $TOKEN"
```

### Skills

```bash
# List skills
curl http://localhost:8080/api/skills \
  -H "Authorization: Bearer $TOKEN"

# Sync skills from filesystem
curl -X POST http://localhost:8080/api/skills/sync \
  -H "Authorization: Bearer $TOKEN"
```

### Alert Sources

```bash
# List available source types
curl http://localhost:8080/api/alert-source-types \
  -H "Authorization: Bearer $TOKEN"

# List configured alert sources
curl http://localhost:8080/api/alert-sources \
  -H "Authorization: Bearer $TOKEN"

# Create an alert source
curl -X POST http://localhost:8080/api/alert-sources \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "source_type_name": "alertmanager",
    "name": "Production Alertmanager",
    "webhook_secret": "optional-secret"
  }'
```

### Webhook Endpoints

Each alert source instance gets a unique webhook URL:

```
POST /webhook/alert/{instance_uuid}
```

Configure this URL in your monitoring system. The webhook automatically normalizes incoming alerts and creates incidents.

## Development

### Project Structure

```
akmatori/
├── cmd/akmatori/          # API server entrypoint
├── internal/
│   ├── alerts/            # Alert adapters for each source type
│   │   └── adapters/      # Alertmanager, Zabbix, PagerDuty, etc.
│   ├── config/            # Configuration loading
│   ├── database/          # GORM models and database logic
│   ├── executor/          # LLM execution engine (legacy)
│   ├── handlers/          # HTTP & WebSocket handlers
│   ├── middleware/        # Auth, CORS middleware
│   ├── services/          # Business logic
│   └── slack/             # Slack integration
│
├── codex-worker/          # Codex Worker container (Go)
│   ├── cmd/worker/        # Worker entrypoint
│   └── internal/
│       ├── codex/         # Codex CLI runner
│       ├── orchestrator/  # Task orchestration
│       └── ws/            # WebSocket client
│
├── mcp-gateway/           # MCP Gateway container (Go)
│   ├── cmd/gateway/       # Gateway entrypoint
│   └── internal/
│       ├── database/      # Credential fetching
│       ├── mcp/           # MCP protocol server
│       └── tools/         # SSH, Zabbix implementations
│
├── codex-tools/           # Python tool wrappers for Codex
│   ├── mcp_client.py      # MCP client library
│   ├── ssh/               # SSH tool (calls MCP Gateway)
│   └── zabbix/            # Zabbix tool (calls MCP Gateway)
│
├── web/                   # React frontend
│   └── src/
│       ├── components/    # Reusable UI components
│       ├── pages/         # Page components
│       └── api/           # API client
│
├── proxy/                 # Nginx configuration
├── docker-compose.yml     # 4-container orchestration
├── Dockerfile.api         # API container
└── Dockerfile.codex       # Codex Worker container
```

## Tech Stack

**Backend Services (Go 1.24+):**
- **API Container**: Incident management, skill orchestration, WebSocket server
- **Codex Worker**: Codex CLI execution, task streaming
- **MCP Gateway**: Tool credential management, SSH/Zabbix execution
- PostgreSQL with GORM, JWT authentication

**Frontend:**
- React 19 with TypeScript
- Vite
- Tailwind CSS

**Infrastructure:**
- Docker & Docker Compose (4-container architecture)
- Nginx reverse proxy
- Network isolation (frontend, api-internal, codex-network)

**AI Execution:**
- [Codex CLI](https://github.com/openai/codex) - OpenAI's open-source AI coding agent
- [Agent Skills](https://github.com/agentskills/agentskills) - Open format for portable AI agent capabilities
- MCP (Model Context Protocol) for secure tool access

## How It Works

Akmatori uses [OpenAI Codex CLI](https://github.com/openai/codex) in an isolated container to execute AI-powered automation tasks. When an alert is received or a skill is triggered:

1. **Alert normalization** - API container extracts key fields using source-specific adapters
2. **Incident creation** - Records context, creates workspace with skill files and symlinks
3. **Task dispatch** - API sends task + OpenAI credentials to Codex Worker via WebSocket
4. **AI execution** - Codex Worker runs Codex CLI in the incident workspace
5. **Tool calls** - When Codex needs SSH/Zabbix access, Python wrappers call MCP Gateway
6. **Credential fetch** - MCP Gateway retrieves credentials from database and executes the operation
7. **Result streaming** - Output streams back through WebSocket to API for real-time updates
8. **Completion** - Results posted to Slack (if configured) and incident status updated

This architecture ensures the AI agent never has direct access to sensitive credentials.

## Supported Alert Sources

| Source | Field Mappings | Notes |
|--------|----------------|-------|
| Alertmanager | labels.alertname, labels.severity, annotations.* | Prometheus-style alerts |
| Zabbix | alert_name, priority, hardware, event_status | Native Zabbix webhooks |
| PagerDuty | event.data.title, event.data.priority | PagerDuty Events API v2 |
| Grafana | ruleName, state, message | Grafana Alerting webhooks |
| Datadog | title, priority, alert_type | Datadog webhooks |

Each adapter normalizes alerts to a common format with configurable field mappings.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
