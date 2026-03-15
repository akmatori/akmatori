import { describe, it, expect } from "vitest";
import { existsSync } from "fs";
import { resolve } from "path";

const TOOLS_DIR = resolve(__dirname, "../tools");

describe("Python tool wrappers removal", () => {
  it("should not have a tools/ directory", () => {
    expect(existsSync(TOOLS_DIR)).toBe(false);
  });

  it("should not have mcp_client.py", () => {
    expect(existsSync(resolve(TOOLS_DIR, "mcp_client.py"))).toBe(false);
  });

  it("should not have ssh/__init__.py", () => {
    expect(existsSync(resolve(TOOLS_DIR, "ssh/__init__.py"))).toBe(false);
  });

  it("should not have zabbix/__init__.py", () => {
    expect(existsSync(resolve(TOOLS_DIR, "zabbix/__init__.py"))).toBe(false);
  });

  it("should not have victoriametrics/__init__.py", () => {
    expect(existsSync(resolve(TOOLS_DIR, "victoriametrics/__init__.py"))).toBe(false);
  });
});
