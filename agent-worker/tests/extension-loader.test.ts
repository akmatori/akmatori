/**
 * Integration test for pi-mono's DefaultResourceLoader.
 *
 * Task 1 of the QMD-replacement plan flips `noExtensions: false` in
 * agent-runner.ts so the loader auto-discovers extensions from
 * agentDir/extensions and the additionalExtensionPaths list. This test
 * exercises the real loader against a tmpdir to guarantee that the option
 * actually causes a sibling extension file to load, so we catch regressions
 * if the SDK changes the option semantics.
 */
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { DefaultResourceLoader } from "@earendil-works/pi-coding-agent";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

describe("DefaultResourceLoader extension discovery", () => {
  let tmpDir: string;
  let agentDir: string;
  let cwd: string;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "akmatori-loader-"));
    agentDir = path.join(tmpDir, "agent");
    cwd = path.join(tmpDir, "cwd");
    fs.mkdirSync(path.join(agentDir, "extensions", "demo"), { recursive: true });
    fs.mkdirSync(cwd, { recursive: true });

    // Minimal extension factory. The body deliberately avoids touching pi-mono
    // APIs that would require a runtime; the loader only needs to import the
    // file and call the default export with the ExtensionAPI stub.
    const extSource = `
      export default function (pi) {
        pi.registerCommand("akmatori-loader-probe", {
          description: "probe",
          handler: async () => {},
        });
      }
    `;
    fs.writeFileSync(path.join(agentDir, "extensions", "demo", "index.ts"), extSource);
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it("loads the extension when noExtensions is false", async () => {
    const loader = new DefaultResourceLoader({
      cwd,
      agentDir,
      noExtensions: false,
      noPromptTemplates: true,
      noThemes: true,
      noContextFiles: true,
    });
    await loader.reload();
    const result = loader.getExtensions();
    expect(result.errors).toEqual([]);
    expect(result.extensions.length).toBeGreaterThan(0);
    const found = result.extensions.find((e) =>
      e.resolvedPath?.includes(path.join("extensions", "demo")),
    );
    expect(found, "demo extension should be discovered under agentDir/extensions").toBeDefined();
  });

  it("skips extension discovery when noExtensions is true (regression guard)", async () => {
    const loader = new DefaultResourceLoader({
      cwd,
      agentDir,
      noExtensions: true,
      noPromptTemplates: true,
      noThemes: true,
      noContextFiles: true,
    });
    await loader.reload();
    const result = loader.getExtensions();
    expect(result.extensions).toEqual([]);
  });
});
