# Akmatori

Akmatori is an AI-powered AIOps agent that integrates with monitoring systems and Slack to provide intelligent incident response and automated remediation.

<img width="1436" height="659" alt="image" src="https://github.com/user-attachments/assets/b2c78bf5-9e20-47da-8ec6-b841c6a0a3de" />

## Quick Start

### Prerequisites

- Docker and Docker Compose v2+
- OpenAI API key ([get one here](https://platform.openai.com/api-keys))
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


## Features

- **Multi-Source Alert Ingestion**: Receive alerts from Alertmanager, PagerDuty, Grafana, Datadog, Zabbix and via webhooks
- **Slack Integration**: Post incidents to channels, receive commands, and provide real-time updates
- **LLM-Powered Automation**: Use OpenAI's models via [Codex CLI](https://github.com/openai/codex) to analyze incidents and execute remediation skills
- **[Agent Skills](https://github.com/agentskills/agentskills) Format**: Skills follow the open Agent Skills specification for portability across AI agents
- **Tools Management**: Configure reusable tools (Python scripts, API clients) for skills
- **Web Dashboard**: Manage incidents, skills, tools, and settings through a modern UI
- **Context Files**: Upload reference documents for the AI to use during incident analysis

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
