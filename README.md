<div align="center">

# Akmatori

**Self-hosted, AI-powered AIOps platform for SRE teams.**

Ingests alerts from your monitoring stack, investigates them with LLM agents that reach your
infrastructure through real tools, and executes remediation behind approval gates.

[![GitHub release](https://img.shields.io/github/v/release/akmatori/akmatori)](https://github.com/akmatori/akmatori/releases/latest)
[![License](https://img.shields.io/github/license/akmatori/akmatori)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)
[![Docker](https://img.shields.io/badge/images-GHCR-2496ED?logo=docker&logoColor=white)](https://github.com/orgs/akmatori/packages)

[Website](https://akmatori.com) · [Documentation](https://akmatori.com/docs) · [Quick Start](#quick-start) · [Architecture](#architecture) · [Issues](https://github.com/akmatori/akmatori/issues)

<img width="1436" height="659" alt="Akmatori incident dashboard" src="https://github.com/user-attachments/assets/b2c78bf5-9e20-47da-8ec6-b841c6a0a3de" />

</div>

## How It Works

1. **An alert fires** — from an Alertmanager, PagerDuty, Grafana, Datadog, or Zabbix webhook, a monitored Slack channel, or a cron schedule.
2. **An agent investigates** — a full LLM agent session queries your infrastructure through gateway tools (SSH, Kubernetes, Zabbix, Grafana, VictoriaMetrics, …) and consults your runbooks and cross-incident memory via subagents.
3. **Remediation waits for you** — risky actions become proposals you approve in the UI or Slack before anything executes.
4. **Results land where you work** — findings post back to the dashboard and your Slack channels, and durable learnings are written to memory for the next incident.

## Key Features

- **Multi-LLM**: OpenAI, Anthropic, Google, OpenRouter, NVIDIA NIM, MiniMax, Ant Ling, or any OpenAI-compatible on-prem endpoint (GLM, Kimi, Mistral, LLaMA, …)
- **Multi-source alert ingestion**: Alertmanager, PagerDuty, Grafana, Datadog, and Zabbix webhooks, plus Slack channel monitoring
- **Agentic investigation**: every incident runs a full agent session with subagents for runbook lookup and cross-incident memory recall
- **Approval-gated remediation**: risky actions become proposals you approve in the UI or Slack before anything executes
- **Messaging channels**: configure providers (Slack today, Telegram on the roadmap) under Settings → Integrations, then attach Channels with post / listen / default capability flags that alert sources and cron jobs reference
- **Cron jobs**: schedule recurring agent investigations that post results to a Channel, each with its own prompt and per-cron tool allowlist
- **[Agent Skills](https://github.com/agentskills/agentskills) format**: skills follow the open Agent Skills specification for portability across AI agents
- **Tools management**: configure reusable tools (SSH, scripts, API clients) and scope them per skill or cron
- **Context files**: upload reference documents the agent uses during incident analysis
- **Web dashboard**: manage incidents, skills, tools, and settings through a modern, mobile-responsive UI
- **Self-hosted**: your data never leaves your infrastructure

## Supported LLM Providers

| Provider | Models |
|----------|--------|
| **OpenAI** | GPT-5.6 (Terra / Sol / Luna), GPT-5.5, GPT-5.5 Pro, GPT-5.4, GPT-5.4 Mini, GPT-5.3 Codex, o4-mini |
| **Anthropic** | Claude Fable 5, Sonnet 5, Opus 4.8 / 4.7, Sonnet 4.6, Haiku 4.5 |
| **Google** | Gemini 3.1 Pro Preview, Gemini 3 Pro / Flash Preview, Gemini 2.5 Pro / Flash, 2.0 Flash |
| **OpenRouter** | 100+ models from every major lab |
| **NVIDIA NIM** | Llama 3.3 / 3.1 70B Instruct, Nemotron 3 Super / Nano |
| **MiniMax** | MiniMax M3, M2.7, M2.7-highspeed |
| **Ant Ling** | Ling-2.6-1T, Ling-2.6-flash, Ring-2.6-1T |
| **Custom / On-prem** | Any OpenAI-compatible endpoint (GLM, Kimi, Mistral, LLaMA, vLLM, …) |

Model lists track the in-app picker; the model field is free-text, so newer model IDs work without an upgrade.

## Quick Start

The recommended install pulls pre-built multi-arch images from GHCR — no `git clone`, no local build.

**Prerequisites:** Docker with Compose v2+, an API key for any supported LLM provider (or an on-prem OpenAI-compatible endpoint), and optionally a Slack App.

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

   `AKMATORI_VERSION` defaults to `latest`. See [Behind an HTTP proxy](#behind-an-http-proxy) for proxy details.

3. Pull and start, then verify all services show "Up":

   ```bash
   docker compose pull
   docker compose up -d
   docker compose ps
   ```

4. Open the web dashboard at `http://localhost:8080` (username `admin`). The first visit runs a one-time setup wizard that lets you set the admin password.

5. Configure your LLM provider in **Settings → LLM Provider**.

6. (Optional) Connect Slack:
   - **Settings → Integrations → Add Slack** to register your bot/signing/app tokens.
   - **Settings → Channels → Add Channel** to attach posting and listening destinations. Tick `is_default_post` on the channel that should receive alerts when an alert source does not pin one explicitly. `can_post` / `can_listen` decide whether the channel appears in the alert-source picker or is monitored for inbound mentions.

7. (Optional) Schedule recurring runs in **Cron Jobs**: pick a schedule, write a prompt, attach the tools the agent is allowed to call, and point it at one of your Channels. Every tick runs as a full agent investigation under the `cron-agent` system skill.

### Upgrade

Bump `AKMATORI_VERSION` in `.env` (or leave it unset to track `latest`) and:

```bash
docker compose pull
docker compose up -d
```

### Behind an HTTP proxy

<details>
<summary>Corporate proxy setup — image pulls and runtime egress</summary>

There are two independent concerns: pulling images through the proxy (a Docker daemon setting) and the running services egressing through the proxy at runtime (a compose `environment:` setting). Both must be configured or you'll get stuck partway through.

#### A. Pulling images through the proxy

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

#### B. Runtime egress through the proxy

Set `HTTP_PROXY` / `HTTPS_PROXY` once in your `.env` (or in the shell that runs `docker compose up`); the `api`, `mcp-gateway`, and `agent` containers inherit them via the compose file. The default `NO_PROXY` bypasses internal service-to-service traffic (api↔postgres, agent↔gateway, etc.) so internal hops never hit the corporate proxy.

```dotenv
# .env
HTTP_PROXY=http://proxy.corp:3128
HTTPS_PROXY=http://proxy.corp:3128
# NO_PROXY defaults to the internal service names; override only if you need to add hosts.
```

The runtime `HTTP_PROXY` covers the API server's outbound calls (Slack), the agent worker's LLM API calls, and the MCP Gateway's HTTP-connector tools and external MCP-server connections. The MCP Gateway's built-in monitoring/CMDB tools (Zabbix, Grafana, VictoriaMetrics, PagerDuty, NetBox, Kubernetes, Catchpoint, Jira) ignore the env-var proxy by design and have their own per-tool proxy toggle in **Settings → Proxy** — enable those if your monitoring endpoints also need to go through the corporate proxy.

</details>

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

## Development

Working on Akmatori itself? Build from source with the dev override, which restores the per-service `build:` blocks:

```bash
git clone https://github.com/akmatori/akmatori.git
cd akmatori
cp .env.example .env   # edit ADMIN_PASSWORD / POSTGRES_PASSWORD
make dev               # docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

`make dev` is the canonical maintainer entry point. The base `docker-compose.yml` alone has only `image:` references (the end-user pull flow); the `docker-compose.dev.yml` override adds the `build:` blocks back. Without the `-f docker-compose.dev.yml` argument, `docker compose build` is a no-op against a release install.

## Documentation

- [Getting Started](https://akmatori.com/docs/getting-started)
- [Architecture](https://akmatori.com/docs/architecture)
- [Alert Integrations](https://akmatori.com/docs/integrations)
- [API Reference](https://akmatori.com/docs/api)
- [Skills Guide](https://akmatori.com/docs/skills)

**Self-hosted API docs:** every install serves interactive documentation — Swagger UI at `http://localhost:8080/api/docs` and the raw OpenAPI 3.1 spec at `http://localhost:8080/api/openapi.yaml` (no authentication required).

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

Licensed under the [Apache License 2.0](LICENSE).
