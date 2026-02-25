import { describe, it, expect } from "vitest";
import { readFileSync, existsSync } from "fs";
import { resolve } from "path";

const TOOLS_DIR = resolve(__dirname, "../tools");

describe("Python tool wrappers", () => {
  describe("mcp_client.py", () => {
    it("should exist", () => {
      expect(existsSync(resolve(TOOLS_DIR, "mcp_client.py"))).toBe(true);
    });

    it("should define MCPClient class", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "mcp_client.py"), "utf-8");
      expect(content).toContain("class MCPClient");
    });

    it("should define call() convenience function", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "mcp_client.py"), "utf-8");
      expect(content).toContain("def call(tool_name:");
    });

    it("should read MCP_GATEWAY_URL from env", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "mcp_client.py"), "utf-8");
      expect(content).toContain("MCP_GATEWAY_URL");
    });

    it("should read INCIDENT_ID from env", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "mcp_client.py"), "utf-8");
      expect(content).toContain("INCIDENT_ID");
    });

    it("should send X-Incident-ID header", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "mcp_client.py"), "utf-8");
      expect(content).toContain("X-Incident-ID");
    });
  });

  describe("ssh/__init__.py", () => {
    it("should exist", () => {
      expect(existsSync(resolve(TOOLS_DIR, "ssh/__init__.py"))).toBe(true);
    });

    it("should export execute_command", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "ssh/__init__.py"), "utf-8");
      expect(content).toContain("def execute_command(");
    });

    it("should export test_connectivity", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "ssh/__init__.py"), "utf-8");
      expect(content).toContain("def test_connectivity(");
    });

    it("should export get_server_info", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "ssh/__init__.py"), "utf-8");
      expect(content).toContain("def get_server_info(");
    });

    it("should accept tool_instance_id on all functions", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "ssh/__init__.py"), "utf-8");
      const functions = content.match(/def \w+\([^)]*\)/g) || [];
      const publicFunctions = functions.filter((f) => !f.startsWith("def _"));
      for (const fn of publicFunctions) {
        expect(fn).toContain("tool_instance_id");
      }
    });

    it("should call MCP Gateway tool names with ssh. prefix", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "ssh/__init__.py"), "utf-8");
      expect(content).toContain('"ssh.execute_command"');
      expect(content).toContain('"ssh.test_connectivity"');
      expect(content).toContain('"ssh.get_server_info"');
    });
  });

  describe("zabbix/__init__.py", () => {
    it("should exist", () => {
      expect(existsSync(resolve(TOOLS_DIR, "zabbix/__init__.py"))).toBe(true);
    });

    it("should export get_hosts", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "zabbix/__init__.py"), "utf-8");
      expect(content).toContain("def get_hosts(");
    });

    it("should export get_problems", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "zabbix/__init__.py"), "utf-8");
      expect(content).toContain("def get_problems(");
    });

    it("should export get_history", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "zabbix/__init__.py"), "utf-8");
      expect(content).toContain("def get_history(");
    });

    it("should export get_items_batch", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "zabbix/__init__.py"), "utf-8");
      expect(content).toContain("def get_items_batch(");
    });

    it("should export acknowledge_event", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "zabbix/__init__.py"), "utf-8");
      expect(content).toContain("def acknowledge_event(");
    });

    it("should accept tool_instance_id on all functions", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "zabbix/__init__.py"), "utf-8");
      const functions = content.match(/def \w+\([^)]*\)/gs) || [];
      const publicFunctions = functions.filter((f) => !f.includes("def _"));
      for (const fn of publicFunctions) {
        expect(fn).toContain("tool_instance_id");
      }
    });

    it("should call MCP Gateway tool names with zabbix. prefix", () => {
      const content = readFileSync(resolve(TOOLS_DIR, "zabbix/__init__.py"), "utf-8");
      expect(content).toContain('"zabbix.get_hosts"');
      expect(content).toContain('"zabbix.get_problems"');
      expect(content).toContain('"zabbix.get_history"');
      expect(content).toContain('"zabbix.get_items_batch"');
      expect(content).toContain('"zabbix.acknowledge_event"');
    });
  });
});
