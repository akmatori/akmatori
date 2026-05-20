# Akmatori

Akmatori is an AI-powered AIOps agent that integrates with monitoring systems and Slack to provide intelligent incident response and automated remediation.

<img width="1436" height="659" alt="image" src="https://github.com/user-attachments/assets/b2c78bf5-9e20-47da-8ec6-b841c6a0a3de" />

## Key Features

- **Multi-LLM Support**: Use OpenAI, Anthropic, Google, OpenRouter, or on-premise models (GLM, Kimi, Minimax, Mistral, LLaMA)
- **Multi-Source Alert Ingestion**: Receive alerts from Alertmanager, PagerDuty, Grafana, Datadog, Zabbix, and Slack channels
- **Messaging Integrations & Channels**: Configure one or more messaging providers (Slack today, Telegram on the roadmap) under Settings → Integrations, then attach Channels with capability flags (post / listen / default) that alert sources and cron jobs reference by UUID
- **Cron Jobs**: Schedule recurring agent investigations that post results to a Channel — pick a 5-field cron expression, write a prompt, and attach a per-cron tool allowlist. Every tick runs as a full investigation under the `cron-agent` system skill; platform-seeded crons (e.g. `memory-curator`) are marked `is_system` and cannot be deleted, only disabled
- **AI-Powered Automation**: Analyze incidents and execute remediation skills using your preferred LLM
- **[Agent Skills](https://github.com/agentskills/agentskills) Format**: Skills follow the open Agent Skills specification for portability across AI agents
- **Tools Management**: Configure reusable tools (SSH, Python scripts, API clients) for skills
- **Web Dashboard**: Manage incidents, skills, tools, and settings through a modern UI
- **Context Files**: Upload reference documents for the AI to use during incident analysis
- **Self-Hosted**: Your data never leaves your infrastructure

## Supported LLM Providers

| Provider | Models |
|----------|--------|
| **OpenAI** | GPT-5.5, GPT-5.5 Pro, GPT-5.4, GPT-5.4 Mini, GPT-5.3 Codex, o4-mini |
| **Anthropic** | Claude Opus 4.7, Opus 4.6, Sonnet 4.6, Haiku 4.5 |
| **Google** | Gemini 3 Pro Preview, Gemini 3.1 Pro Preview, Gemini 3 Flash Preview, Gemini 2.5 Pro/Flash, 2.0 Flash |
| **OpenRouter** | Access to 100+ models |
| **Custom/On-Prem** | GLM, Kimi, Minimax, Mistral, LLaMA, etc. |

## Install (end users)

The recommended install flow pulls pre-built multi-arch images from GHCR — no `git clone`, no local build.

### Prerequisites

- Docker and Docker Compose v2+
- LLM API key (OpenAI, Anthropic, Google, or OpenRouter)
- Slack App (optional, for Slack integration)

### Install

1. Download the release assets (compose file + nginx config):
   ```bash
   mkdir akmatori && cd akmatori
   curl -fsSLO https://github.com/akmatori/akmatori/releases/latest/download/docker-compose.yml
   mkdir proxy && curl -fsSL -o proxy/nginx.conf \
     https://github.com/akmatori/akmatori/releases/latest/download/nginx.conf
   ```

2. (Optional) Create an `.env` to pin a specific version or configure a corporate proxy. All secrets (`POSTGRES_PASSWORD`, `JWT_SECRET`, and the admin password) are auto-generated on first run, so the file is only needed for the overrides shown below:
   ```bash
   cat > .env <<'EOF'
   # AKMATORI_VERSION=1.2.0
   # HTTP_PROXY=http://proxy.corp:3128
   # HTTPS_PROXY=http://proxy.corp:3128
   EOF
   ```

   `AKMATORI_VERSION` defaults to `latest`. See the "Behind an HTTP proxy" section below for proxy details.

3. Pull and start:
   ```bash
   docker compose pull
   docker compose up -d
   ```

4. Verify all containers are running:
   ```bash
   docker compose ps
   ```
   All services should show "Up" status.

5. Access the web dashboard at `http://localhost:8080` (username `admin`). The first visit runs a one-time setup wizard that lets you set the admin password.

6. Configure your LLM provider in **Settings → LLM Provider**

7. (Optional) Connect Slack:
   - **Settings → Integrations → Add Slack** to register your bot/signing/app tokens.
   - **Settings → Channels → Add Channel** to attach posting and listening destinations. Tick `is_default_post` on the channel that should receive alerts when an alert source does not pin one explicitly. `can_post` / `can_listen` decide whether the channel appears in the alert-source picker or is monitored for inbound mentions.

8. (Optional) Schedule recurring runs in **Cron Jobs**: pick a schedule, write a prompt, attach the tools the agent is allowed to call, and point it at one of your Channels. Every tick runs as a full agent investigation under the `cron-agent` system skill.

### Upgrade

Bump `AKMATORI_VERSION` in `.env` (or leave it unset to track `latest`) and:

```bash
docker compose pull
docker compose up -d
```

## Behind an HTTP proxy

There are two independent concerns: pulling images through the proxy (a Docker daemon setting) and the running services egressing through the proxy at runtime (a compose `environment:` setting). Both must be configured or you'll get stuck partway through.

### A. Pulling images through the proxy

This is a Docker daemon-level setting — compose can't fix it from inside the file.

**Linux + systemd:**

```ini
# /etc/systemd/system/docker.service.d/http-proxy.conf
[Service]
Environment="HTTP_PROXY=http://proxy.corp:3128"
Environment="HTTPS_PROXY=http://proxy.corp:3128"
Environment="NO_PROXY=localhost,127.0.0.1,.svc,.local"
```

Then:

```bash
sudo systemctl daemon-reload
sudo systemctl restart docker
```

**Docker Desktop (macOS / Windows):** Settings → Resources → Proxies.

**Allowlist note:** your corporate proxy allowlist must include `pkg-containers.githubusercontent.com` alongside `ghcr.io`. GHCR stores image manifests on `ghcr.io` and the actual blob layers on `pkg-containers.githubusercontent.com` — the most common GHCR-through-corporate-proxy footgun is "manifest pulls but blob downloads hang."

### B. Runtime egress through the proxy

Set `HTTP_PROXY` / `HTTPS_PROXY` once in your `.env` (or in the shell that runs `docker compose up`); the `api`, `mcp-gateway`, and `agent` containers inherit them via the compose file. The default `NO_PROXY` bypasses internal service-to-service traffic (api↔postgres, agent↔gateway, etc.) so internal hops never hit the corporate proxy.

```dotenv
# .env
HTTP_PROXY=http://proxy.corp:3128
HTTPS_PROXY=http://proxy.corp:3128
# NO_PROXY defaults to the internal service names; override only if you need to add hosts.
```

The runtime `HTTP_PROXY` covers the API server's outbound calls (Slack), the agent worker's LLM API calls, and the MCP Gateway's HTTP-connector tools and external MCP-server connections. The MCP Gateway's built-in monitoring/CMDB tools (Zabbix, Grafana, VictoriaMetrics, PagerDuty, NetBox, Kubernetes, Catchpoint, Jira) ignore the env-var proxy by design and have their own per-tool proxy toggle in **Settings → Proxy** — enable those if your monitoring endpoints also need to go through the corporate proxy.

## Maintainer / development

If you're working on Akmatori itself and want to build from source instead of pulling published images, use the dev override which restores the per-service `build:` blocks:

```bash
git clone https://github.com/akmatori/akmatori.git
cd akmatori
cp .env.example .env   # edit ADMIN_PASSWORD / POSTGRES_PASSWORD
make dev               # docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

`make dev` is the canonical maintainer entry point. The base `docker-compose.yml` alone has only `image:` references (the end-user pull flow); the `docker-compose.dev.yml` override adds the `build:` blocks back. Without the `-f docker-compose.dev.yml` argument, `docker compose build` is a no-op against a release install.

## Architecture

Akmatori uses a secure multi-container architecture:

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
└─────────────────┘     │  (pi-mono +     │     │  (SSH, APIs)    │
                        │   subagents)    │     │                 │
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

**Runbook and memory search:** runbooks and cross-incident memory live as markdown files mounted into the agent container (`runbooks` read-only, `memory` read-write). The main agent reaches them through pi-mono subagents — `runbook-searcher` for SOP lookup, `memory-searcher` for prior-incident recall, and `memory-writer` to record durable findings at the end of an investigation. The API materializes new memory files back into Postgres at incident completion.

## Documentation

- [Getting Started](https://akmatori.com/docs/getting-started)
- [Architecture](https://akmatori.com/docs/architecture)
- [Alert Integrations](https://akmatori.com/docs/integrations)
- [API Reference](https://akmatori.com/docs/api)
- [Skills Guide](https://akmatori.com/docs/skills)

### API Documentation (Self-Hosted)

The API server includes built-in interactive documentation:

- **Swagger UI**: `http://localhost:8080/api/docs` — browse and test API endpoints in your browser
- **OpenAPI Spec**: `http://localhost:8080/api/openapi.yaml` — raw OpenAPI 3.1 specification

Both endpoints are publicly accessible (no authentication required).

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Community

- [Website](https://akmatori.com)
- [Documentation](https://akmatori.com/docs)
- [GitHub Issues](https://github.com/akmatori/akmatori/issues)
