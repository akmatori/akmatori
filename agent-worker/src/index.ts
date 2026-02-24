/**
 * Agent worker entry point.
 *
 * Reads configuration from environment variables, creates the Orchestrator,
 * connects to the API WebSocket with retry logic, and handles graceful shutdown.
 *
 * Ports the Go codex-worker main.go entry point to Node.js.
 */

import { Orchestrator, type OrchestratorConfig } from "./orchestrator.js";

// ---------------------------------------------------------------------------
// Configuration from environment
// ---------------------------------------------------------------------------

const API_WS_URL = process.env.API_WS_URL ?? "ws://akmatori-api:3000/ws/codex";
const MCP_GATEWAY_URL = process.env.MCP_GATEWAY_URL ?? "http://mcp-gateway:8080";
const WORKSPACE_DIR = process.env.WORKSPACE_DIR ?? "/workspaces";
const SKILLS_DIR = process.env.SKILLS_DIR ?? "/akmatori/skills";

const RECONNECT_DELAY_MS = 5_000;

// ---------------------------------------------------------------------------
// Logger
// ---------------------------------------------------------------------------

function log(msg: string): void {
  const ts = new Date().toISOString();
  console.log(`[agent-worker] ${ts} ${msg}`);
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main(): Promise<void> {
  log("Starting Agent Worker...");
  log(`  API_WS_URL:     ${API_WS_URL}`);
  log(`  MCP_GATEWAY_URL: ${MCP_GATEWAY_URL}`);
  log(`  WORKSPACE_DIR:   ${WORKSPACE_DIR}`);
  log(`  SKILLS_DIR:      ${SKILLS_DIR}`);

  const config: OrchestratorConfig = {
    apiWsUrl: API_WS_URL,
    mcpGatewayUrl: MCP_GATEWAY_URL,
    workspaceDir: WORKSPACE_DIR,
    skillsDir: SKILLS_DIR,
    logger: log,
  };

  const orchestrator = new Orchestrator(config);

  // Connect with retry (forever until success, matching Go behavior)
  await connectWithRetry(orchestrator);

  // Handle shutdown signals
  let shuttingDown = false;

  const shutdown = async () => {
    if (shuttingDown) return;
    shuttingDown = true;
    log("Received shutdown signal");
    await orchestrator.stop();
    log("Agent Worker stopped");
    process.exit(0);
  };

  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);

  // Monitor connection and reconnect on loss
  monitorConnection(orchestrator);
}

// ---------------------------------------------------------------------------
// Connection management
// ---------------------------------------------------------------------------

async function connectWithRetry(orchestrator: Orchestrator): Promise<void> {
  while (!orchestrator.isStopped()) {
    try {
      await orchestrator.start();
      return;
    } catch (err) {
      log(`Failed to start orchestrator: ${err}. Retrying in ${RECONNECT_DELAY_MS / 1000}s`);
      await sleep(RECONNECT_DELAY_MS);
    }
  }
}

/**
 * Monitor the WebSocket connection and automatically reconnect on loss.
 * Uses a polling interval to detect disconnection.
 */
function monitorConnection(orchestrator: Orchestrator): void {
  const CHECK_INTERVAL_MS = 5_000;

  const timer = setInterval(async () => {
    if (orchestrator.isStopped()) {
      clearInterval(timer);
      return;
    }

    if (!orchestrator.isConnected()) {
      log("Connection lost, attempting reconnect...");
      clearInterval(timer);

      try {
        // Close cleanly and restart
        const wsClient = orchestrator.getWsClient();
        wsClient.reset();
        await connectWithRetry(orchestrator);
      } catch (err) {
        log(`Reconnect failed: ${err}`);
      }

      // Resume monitoring regardless of reconnect success or failure
      if (!orchestrator.isStopped()) {
        monitorConnection(orchestrator);
      }
    }
  }, CHECK_INTERVAL_MS);
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

main().catch((err) => {
  log(`Fatal error: ${err}`);
  process.exit(1);
});
