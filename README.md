# Akmatori

Akmatori is an AI-powered AIOps agent that integrates with monitoring systems and Slack to provide intelligent incident response and automated remediation.

## Features

- **Integrations**: Receive alerts via webhooks
- **Slack Integration**: Post incidents to channels, receive commands, and provide real-time updates
- **LLM-Powered Automation**: Use OpenAI's models to analyze incidents and execute remediation skills
- **Skills System**: Define custom automation skills with prompts and attached tools
- **Tools Management**: Configure reusable tools (Python scripts, API clients) for skills
- **Web Dashboard**: Manage incidents, skills, tools, and settings through a modern UI
- **Context Files**: Upload reference documents for the AI to use during incident analysis

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Alerts    │────▶│  Akmatori   │◀────│    Slack    │
│             │     │   Backend   │     │     Bot     │
└─────────────┘     └──────┬──────┘     └─────────────┘
                           │
                    ┌──────┴──────┐
                    │             │
              ┌─────▼─────┐ ┌─────▼─────┐
              │ PostgreSQL│ │  OpenAI   │
              │    DB     │ │    API    │
              └───────────┘ └───────────┘
```

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
     

## Skills

Skills are AI-powered automation units that can be triggered to handle specific tasks.

### Creating a Skill

1. Navigate to **Skills** in the dashboard
2. Click **Create Skill**
3. Provide:
   - **Name**: Unique identifier (e.g., `disk-cleanup`)
   - **Description**: What the skill does
   - **Category**: Grouping (e.g., `infrastructure`, `database`)
   - **Prompt**: Instructions for the AI

### Attaching Tools

Tools provide skills with capabilities like API access or script execution:

1. Navigate to **Tools** to create tool instances
2. Configure tool settings (API endpoints, credentials)
3. Attach tools to skills via the skill edit page

## Tools

### Built-in Tool Types

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

## Development

### Project Structure

```
akmatori/
├── cmd/akmatori/          # Application entrypoint
├── internal/
│   ├── config/            # Configuration loading
│   ├── database/          # GORM models and database logic
│   ├── executor/          # LLM execution engine
│   ├── handlers/          # HTTP request handlers
│   ├── middleware/        # Auth, CORS middleware
│   ├── models/            # Data structures
│   ├── services/          # Business logic
│   ├── slack/             # Slack integration
│   └── utils/             # Utility functions
├── tools/                 # Python tools
│   └── zabbix/            # Zabbix API client
├── web/                   # React frontend
│   ├── src/
│   │   ├── components/    # Reusable UI components
│   │   ├── pages/         # Page components
│   │   ├── api/           # API client
│   │   └── context/       # React context providers
│   └── ...
├── proxy/                 # Nginx configuration
├── docker-compose.yml
├── Dockerfile
└── Makefile
```

## Tech Stack

**Backend:**
- Go 1.24+
- PostgreSQL with GORM
- JWT authentication

**Frontend:**
- React 19 with TypeScript
- Vite
- Tailwind CSS

**Infrastructure:**
- Docker & Docker Compose
- Nginx reverse proxy

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
