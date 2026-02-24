# Akmatori

Akmatori is an AI-powered AIOps agent that integrates with monitoring systems and Slack to provide intelligent incident response and automated remediation.

<img width="1436" height="659" alt="image" src="https://github.com/user-attachments/assets/b2c78bf5-9e20-47da-8ec6-b841c6a0a3de" />

## Key Features

- **Multi-LLM Support**: Use OpenAI, Anthropic, Google, OpenRouter, or on-premise models (GLM, Kimi, Minimax, Mistral, LLaMA)
- **Multi-Source Alert Ingestion**: Receive alerts from Alertmanager, PagerDuty, Grafana, Datadog, Zabbix, and Slack channels
- **Slack Integration**: Post incidents to channels, receive commands, and provide real-time updates
- **AI-Powered Automation**: Analyze incidents and execute remediation skills using your preferred LLM
- **[Agent Skills](https://github.com/agentskills/agentskills) Format**: Skills follow the open Agent Skills specification for portability across AI agents
- **Tools Management**: Configure reusable tools (SSH, Python scripts, API clients) for skills
- **Web Dashboard**: Manage incidents, skills, tools, and settings through a modern UI
- **Context Files**: Upload reference documents for the AI to use during incident analysis
- **Self-Hosted**: Your data never leaves your infrastructure

## Supported LLM Providers

| Provider | Models |
|----------|--------|
| **OpenAI** | GPT-4o, GPT-4 Turbo, o1, o3 |
| **Anthropic** | Claude 3.5 Sonnet, Claude 3 Opus |
| **Google** | Gemini 2.0, Gemini 1.5 Pro |
| **OpenRouter** | Access to 100+ models |
| **Custom/On-Prem** | GLM, Kimi, Minimax, Mistral, LLaMA, etc. |

## Quick Start

### Prerequisites

- Docker and Docker Compose v2+
- LLM API key (OpenAI, Anthropic, Google, or OpenRouter)
- Slack App (optional, for Slack integration)

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/akmatori/akmatori.git
   cd akmatori
   ```

2. Create and configure environment file:
   ```bash
   cp .env.example .env
   ```

   Edit `.env` and set secure passwords:
   ```bash
   ADMIN_PASSWORD=your-secure-password
   POSTGRES_PASSWORD=your-db-password
   ```

3. Start the services (first run builds containers, takes 3-5 minutes):
   ```bash
   docker-compose up -d
   ```

4. Verify all containers are running:
   ```bash
   docker-compose ps
   ```
   All 5 services should show "Up" status.

5. Access the web dashboard at `http://localhost:8080`
   - **Username:** `admin`
   - **Password:** the `ADMIN_PASSWORD` you set in `.env`

6. Configure your LLM provider in **Settings → LLM Provider**

## Architecture

Akmatori uses a secure 4-container architecture:

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Alert Sources  │────▶│    API Server   │◀───▶│   PostgreSQL    │
│  (Prometheus,   │     │  (Go backend)   │     │   (encrypted    │
│   PagerDuty,    │     │                 │     │   credentials)  │
│   Datadog...)   │     └────────┬────────┘     └─────────────────┘
└─────────────────┘              │
                                 │ WebSocket
┌─────────────────┐              ▼
│  Slack Bot      │◀───▶┌─────────────────┐     ┌─────────────────┐
│                 │     │  Agent Worker   │◀───▶│   MCP Gateway   │
└─────────────────┘     │  (pi-mono)      │     │  (SSH, APIs)    │
                        └────────┬────────┘     └─────────────────┘
                                 │
                                 ▼
                        ┌─────────────────┐
                        │  LLM Providers  │
                        │  (OpenAI,       │
                        │   Anthropic,    │
                        │   Google, etc.) │
                        └─────────────────┘
```

**Security by design:**
- Agent Worker has NO database access
- Credentials are fetched via MCP Gateway on-demand
- Network isolation between containers
- API keys passed per-incident via WebSocket

## Documentation

- [Getting Started](https://akmatori.com/docs/getting-started)
- [Architecture](https://akmatori.com/docs/architecture)
- [Alert Integrations](https://akmatori.com/docs/integrations)
- [API Reference](https://akmatori.com/docs/api)
- [Skills Guide](https://akmatori.com/docs/skills)

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Community

- [Website](https://akmatori.com)
- [Documentation](https://akmatori.com/docs)
- [GitHub Issues](https://github.com/akmatori/akmatori/issues)
