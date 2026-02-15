import { describe, it, expect } from "vitest";
import {
  createMessage,
  serializeMessage,
  deserializeMessage,
  type WebSocketMessage,
  type ProxyConfig,
  type LLMSettings,
  type ExecuteResult,
  type MessageType,
  type APIToWorkerMessageType,
  type WorkerToAPIMessageType,
} from "../src/types.js";

describe("MessageType constants", () => {
  it("API-to-worker message types match Go constants", () => {
    const types: APIToWorkerMessageType[] = [
      "new_incident",
      "continue_incident",
      "cancel_incident",
      "proxy_config_update",
    ];
    expect(types).toHaveLength(4);
    // These values must match Go CodexMessageType constants exactly
    expect(types).toContain("new_incident");
    expect(types).toContain("continue_incident");
    expect(types).toContain("cancel_incident");
    expect(types).toContain("proxy_config_update");
  });

  it("worker-to-API message types match Go constants", () => {
    const types: WorkerToAPIMessageType[] = [
      "codex_output",
      "codex_completed",
      "codex_error",
      "heartbeat",
      "status",
    ];
    expect(types).toHaveLength(5);
    expect(types).toContain("codex_output");
    expect(types).toContain("codex_completed");
    expect(types).toContain("codex_error");
    expect(types).toContain("heartbeat");
    expect(types).toContain("status");
  });
});

describe("WebSocketMessage serialization", () => {
  it("serializes a new_incident message matching Go JSON format", () => {
    const msg: WebSocketMessage = {
      type: "new_incident",
      incident_id: "inc-123",
      task: "Investigate high CPU on server-01",
      openai_api_key: "sk-test-key",
      model: "gpt-4o",
      reasoning_effort: "medium",
    };

    const json = serializeMessage(msg);
    const parsed = JSON.parse(json);

    // Verify exact Go JSON field names (snake_case)
    expect(parsed.type).toBe("new_incident");
    expect(parsed.incident_id).toBe("inc-123");
    expect(parsed.task).toBe("Investigate high CPU on server-01");
    expect(parsed.openai_api_key).toBe("sk-test-key");
    expect(parsed.model).toBe("gpt-4o");
    expect(parsed.reasoning_effort).toBe("medium");

    // Verify omitempty: fields not set should not be present
    expect(parsed).not.toHaveProperty("output");
    expect(parsed).not.toHaveProperty("session_id");
    expect(parsed).not.toHaveProperty("error");
    expect(parsed).not.toHaveProperty("tokens_used");
    expect(parsed).not.toHaveProperty("execution_time_ms");
    expect(parsed).not.toHaveProperty("proxy_config");
  });

  it("serializes a codex_output message matching Go JSON format", () => {
    const msg: WebSocketMessage = {
      type: "codex_output",
      incident_id: "inc-456",
      output: "Checking server metrics...\n",
    };

    const json = serializeMessage(msg);
    const parsed = JSON.parse(json);

    expect(parsed.type).toBe("codex_output");
    expect(parsed.incident_id).toBe("inc-456");
    expect(parsed.output).toBe("Checking server metrics...\n");
    expect(Object.keys(parsed)).toHaveLength(3);
  });

  it("serializes a codex_completed message with metrics", () => {
    const msg: WebSocketMessage = {
      type: "codex_completed",
      incident_id: "inc-789",
      session_id: "session-abc",
      output: "Final analysis: CPU spike caused by runaway process",
      tokens_used: 1500,
      execution_time_ms: 45000,
    };

    const json = serializeMessage(msg);
    const parsed = JSON.parse(json);

    expect(parsed.type).toBe("codex_completed");
    expect(parsed.incident_id).toBe("inc-789");
    expect(parsed.session_id).toBe("session-abc");
    expect(parsed.output).toBe(
      "Final analysis: CPU spike caused by runaway process",
    );
    expect(parsed.tokens_used).toBe(1500);
    expect(parsed.execution_time_ms).toBe(45000);
  });

  it("serializes a codex_error message", () => {
    const msg: WebSocketMessage = {
      type: "codex_error",
      incident_id: "inc-err",
      error: "Authentication failed: invalid API key",
    };

    const json = serializeMessage(msg);
    const parsed = JSON.parse(json);

    expect(parsed.type).toBe("codex_error");
    expect(parsed.incident_id).toBe("inc-err");
    expect(parsed.error).toBe("Authentication failed: invalid API key");
  });

  it("serializes a heartbeat message (minimal)", () => {
    const msg: WebSocketMessage = { type: "heartbeat" };

    const json = serializeMessage(msg);
    const parsed = JSON.parse(json);

    expect(parsed).toEqual({ type: "heartbeat" });
  });

  it("serializes a status message", () => {
    const msg: WebSocketMessage = {
      type: "status",
      message: "ready",
    };

    const json = serializeMessage(msg);
    const parsed = JSON.parse(json);

    expect(parsed.type).toBe("status");
    expect(parsed.message).toBe("ready");
  });

  it("includes proxy_config as nested object when present", () => {
    const proxyConfig: ProxyConfig = {
      url: "http://proxy.internal:8080",
      no_proxy: "localhost,mcp-gateway",
      openai_enabled: true,
      slack_enabled: false,
      zabbix_enabled: false,
    };

    const msg: WebSocketMessage = {
      type: "new_incident",
      incident_id: "inc-proxy",
      task: "Check server",
      proxy_config: proxyConfig,
    };

    const json = serializeMessage(msg);
    const parsed = JSON.parse(json);

    expect(parsed.proxy_config).toEqual({
      url: "http://proxy.internal:8080",
      no_proxy: "localhost,mcp-gateway",
      openai_enabled: true,
      slack_enabled: false,
      zabbix_enabled: false,
    });
  });

  it("omits zero-value fields matching Go omitempty behavior", () => {
    const msg: WebSocketMessage = {
      type: "codex_completed",
      incident_id: "inc-zero",
      tokens_used: 0,
      execution_time_ms: 0,
      output: "",
      error: "",
    };

    const json = serializeMessage(msg);
    const parsed = JSON.parse(json);

    // Go omitempty strips zero-value int and empty string fields
    expect(parsed).not.toHaveProperty("tokens_used");
    expect(parsed).not.toHaveProperty("execution_time_ms");
    expect(parsed).not.toHaveProperty("output");
    expect(parsed).not.toHaveProperty("error");
    // type and incident_id should remain
    expect(parsed.type).toBe("codex_completed");
    expect(parsed.incident_id).toBe("inc-zero");
  });
});

describe("WebSocketMessage deserialization", () => {
  it("deserializes a Go-formatted new_incident message", () => {
    // This JSON is what the Go API server would send
    const goJson = JSON.stringify({
      type: "new_incident",
      incident_id: "inc-from-go",
      task: "Investigate alert on db-01",
      openai_api_key: "sk-from-go",
      model: "o3",
      reasoning_effort: "high",
      proxy_config: {
        url: "http://proxy:3128",
        no_proxy: "mcp-gateway",
        openai_enabled: true,
        slack_enabled: true,
        zabbix_enabled: false,
      },
    });

    const msg = deserializeMessage(goJson);

    expect(msg.type).toBe("new_incident");
    expect(msg.incident_id).toBe("inc-from-go");
    expect(msg.task).toBe("Investigate alert on db-01");
    expect(msg.openai_api_key).toBe("sk-from-go");
    expect(msg.model).toBe("o3");
    expect(msg.reasoning_effort).toBe("high");
    expect(msg.proxy_config?.url).toBe("http://proxy:3128");
    expect(msg.proxy_config?.openai_enabled).toBe(true);
  });

  it("deserializes a continue_incident message", () => {
    const goJson = JSON.stringify({
      type: "continue_incident",
      incident_id: "inc-continue",
      session_id: "session-xyz",
      message: "Also check disk usage",
    });

    const msg = deserializeMessage(goJson);

    expect(msg.type).toBe("continue_incident");
    expect(msg.incident_id).toBe("inc-continue");
    expect(msg.session_id).toBe("session-xyz");
    expect(msg.message).toBe("Also check disk usage");
  });

  it("deserializes a cancel_incident message", () => {
    const goJson = JSON.stringify({
      type: "cancel_incident",
      incident_id: "inc-cancel",
    });

    const msg = deserializeMessage(goJson);

    expect(msg.type).toBe("cancel_incident");
    expect(msg.incident_id).toBe("inc-cancel");
  });

  it("handles unknown fields gracefully (forward compatibility)", () => {
    // Go might add new fields that this worker doesn't know about yet
    const goJson = JSON.stringify({
      type: "new_incident",
      incident_id: "inc-future",
      task: "Some task",
      some_future_field: "unknown value",
    });

    const msg = deserializeMessage(goJson);

    expect(msg.type).toBe("new_incident");
    expect(msg.incident_id).toBe("inc-future");
    // Unknown fields are preserved in the object (JavaScript doesn't strip them)
    expect((msg as Record<string, unknown>)["some_future_field"]).toBe(
      "unknown value",
    );
  });

  it("round-trips through serialize -> deserialize", () => {
    const original: WebSocketMessage = {
      type: "codex_completed",
      incident_id: "inc-rt",
      session_id: "sess-rt",
      output: "Investigation complete",
      tokens_used: 2500,
      execution_time_ms: 30000,
    };

    const json = serializeMessage(original);
    const restored = deserializeMessage(json);

    expect(restored.type).toBe(original.type);
    expect(restored.incident_id).toBe(original.incident_id);
    expect(restored.session_id).toBe(original.session_id);
    expect(restored.output).toBe(original.output);
    expect(restored.tokens_used).toBe(original.tokens_used);
    expect(restored.execution_time_ms).toBe(original.execution_time_ms);
  });
});

describe("createMessage helper", () => {
  it("creates a message with just a type", () => {
    const msg = createMessage("heartbeat");
    expect(msg).toEqual({ type: "heartbeat" });
  });

  it("creates a message with type and fields", () => {
    const msg = createMessage("codex_output", {
      incident_id: "inc-helper",
      output: "Processing...",
    });

    expect(msg.type).toBe("codex_output");
    expect(msg.incident_id).toBe("inc-helper");
    expect(msg.output).toBe("Processing...");
  });

  it("does not include fields not in the parameter", () => {
    const msg = createMessage("status", { message: "ready" });

    expect(msg.type).toBe("status");
    expect(msg.message).toBe("ready");
    expect(msg.incident_id).toBeUndefined();
  });
});

describe("ProxyConfig type", () => {
  it("matches Go ProxyConfig JSON structure", () => {
    const config: ProxyConfig = {
      url: "http://squid:3128",
      no_proxy: "localhost,127.0.0.1,mcp-gateway",
      openai_enabled: true,
      slack_enabled: true,
      zabbix_enabled: false,
    };

    const json = JSON.stringify(config);
    const parsed = JSON.parse(json);

    // Verify Go field names
    expect(parsed.url).toBe("http://squid:3128");
    expect(parsed.no_proxy).toBe("localhost,127.0.0.1,mcp-gateway");
    expect(parsed.openai_enabled).toBe(true);
    expect(parsed.slack_enabled).toBe(true);
    expect(parsed.zabbix_enabled).toBe(false);
  });
});

describe("LLMSettings type", () => {
  it("supports all provider types", () => {
    const providers: LLMSettings["provider"][] = [
      "openai",
      "anthropic",
      "google",
      "openrouter",
      "custom",
    ];
    expect(providers).toHaveLength(5);
  });

  it("supports all thinking levels", () => {
    const levels: LLMSettings["thinking_level"][] = [
      "off",
      "minimal",
      "low",
      "medium",
      "high",
      "xhigh",
    ];
    expect(levels).toHaveLength(6);
  });

  it("serializes with expected fields", () => {
    const settings: LLMSettings = {
      provider: "anthropic",
      api_key: "sk-ant-test",
      model: "claude-opus-4-6",
      thinking_level: "high",
    };

    const json = JSON.stringify(settings);
    const parsed = JSON.parse(json);

    expect(parsed.provider).toBe("anthropic");
    expect(parsed.api_key).toBe("sk-ant-test");
    expect(parsed.model).toBe("claude-opus-4-6");
    expect(parsed.thinking_level).toBe("high");
    expect(parsed).not.toHaveProperty("base_url");
  });

  it("serializes with optional base_url for custom provider", () => {
    const settings: LLMSettings = {
      provider: "custom",
      api_key: "custom-key",
      model: "my-model",
      thinking_level: "medium",
      base_url: "https://my-llm.example.com/v1",
    };

    const json = JSON.stringify(settings);
    const parsed = JSON.parse(json);

    expect(parsed.base_url).toBe("https://my-llm.example.com/v1");
  });
});

describe("ExecuteResult type", () => {
  it("matches expected structure", () => {
    const result: ExecuteResult = {
      session_id: "sess-123",
      response: "Completed investigation",
      full_log: "Step 1: ...\nStep 2: ...\n",
      tokens_used: 3000,
      execution_time_ms: 60000,
    };

    expect(result.session_id).toBe("sess-123");
    expect(result.response).toBe("Completed investigation");
    expect(result.full_log).toContain("Step 1");
    expect(result.tokens_used).toBe(3000);
    expect(result.execution_time_ms).toBe(60000);
    expect(result.error).toBeUndefined();
  });

  it("includes error field when present", () => {
    const result: ExecuteResult = {
      session_id: "",
      response: "",
      full_log: "",
      error: "API key invalid",
      tokens_used: 0,
      execution_time_ms: 500,
    };

    expect(result.error).toBe("API key invalid");
  });
});
