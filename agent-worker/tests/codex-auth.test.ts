import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { resolveModel } from "../src/agent-runner.js";
import { runOneshotLLM } from "../src/oneshot-llm.js";
import {
  CodexAuth,
  CODEX_DEFAULT_MODEL,
  CODEX_PROVIDER,
  createCodexRuntime,
  resolveCodexAccessToken,
} from "../src/codex-auth.js";

let agentDir: string;

beforeEach(async () => {
  agentDir = await mkdtemp(join(tmpdir(), "akmatori-codex-test-"));
  vi.stubEnv("PI_CODING_AGENT_DIR", agentDir);
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  await rm(agentDir, { recursive: true, force: true });
});

it("authorizes by device code, refreshes the stored credential, and disconnects", async () => {
  const loginToken = fakeAccessToken("initial");
  const refreshed = fakeAccessToken("refreshed");
  const approval = Promise.withResolvers<Response>();
  const fetchMock = vi.fn(async (input: string | URL, init?: RequestInit) => {
    switch (new URL(input).pathname) {
      case "/api/accounts/deviceauth/usercode":
        return Response.json({ device_auth_id: "private-device-id", user_code: "TEST-123", interval: 1 });
      case "/api/accounts/deviceauth/token":
        return approval.promise;
      case "/oauth/token": {
        const params = new URLSearchParams(init?.body as URLSearchParams);
        if (params.get("grant_type") === "refresh_token") {
          expect(params.get("refresh_token")).toBe("initial-refresh");
          return Response.json({ access_token: refreshed, refresh_token: "new-refresh", expires_in: 3600 });
        }
        expect(params.get("grant_type")).toBe("authorization_code");
        return Response.json({ access_token: loginToken, refresh_token: "initial-refresh", expires_in: 30 });
      }
      case "/backend-api/codex/responses": {
        const headers = new Headers(init?.headers);
        expect(new URL(input).hostname).toBe("chatgpt.com");
        expect(headers.get("authorization")).toBe(`Bearer ${refreshed}`);
        expect(headers.get("chatgpt-account-id")).toBe("test-account");
        return new Response([
          {
            type: "response.output_item.done", output_index: 0,
            item: { type: "message", id: "msg_1", content: [{ type: "output_text", text: "Incident title" }] },
          },
          { type: "response.completed", response: { id: "resp_1", status: "completed" } },
        ].map((event) => `data: ${JSON.stringify(event)}\n\n`).join(""), {
          headers: { "Content-Type": "text/event-stream" },
        });
      }
      default:
        throw new Error(`Unexpected request: ${input}`);
    }
  });
  vi.stubGlobal("fetch", fetchMock);

  const auth = new CodexAuth();
  expect((await auth.handle("start")).pending).toBe(true);
  try {
    await vi.waitFor(async () => {
      expect(await auth.handle("status")).toEqual({
        connected: false, pending: true, user_code: "TEST-123", error: undefined,
      });
    });
    await auth.handle("start");
    expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/usercode"))).toHaveLength(1);
  } finally {
    approval.resolve(Response.json({ authorization_code: "test-code", code_verifier: "test-verifier" }));
  }

  const restarted = new CodexAuth();
  await vi.waitFor(async () => {
    expect(await restarted.handle("status")).toEqual({
      connected: true, pending: false, user_code: undefined, error: undefined,
    });
  });
  expect((await stat(join(agentDir, "akmatori-codex-auth.json"))).mode & 0o777).toBe(0o600);

  const model = resolveModel(CODEX_PROVIDER, CODEX_DEFAULT_MODEL, "https://other.invalid");
  expect(model.api).toBe("openai-codex-responses");
  expect(model.baseUrl).toBe("https://chatgpt.com/backend-api");
  expect(await resolveCodexAccessToken(await createCodexRuntime(), model)).toBe(refreshed);
  expect(await resolveCodexAccessToken(await createCodexRuntime(), model)).toBe(refreshed);
  expect(fetchMock.mock.calls.filter(([, init]) => String(init?.body).includes("grant_type=refresh_token"))).toHaveLength(1);
  await expect(runOneshotLLM({
    requestId: "test-codex",
    llmSettings: { provider: CODEX_PROVIDER, model: model.id, api_key: "", thinking_level: "medium" },
    user: "Give this incident a title",
    maxTokens: 50,
    temperature: 0.3,
  })).resolves.toBe("Incident title");

  expect((await restarted.handle("disconnect")).connected).toBe(false);
  await expect(resolveCodexAccessToken(await createCodexRuntime(), model)).rejects.toThrow("Connect ChatGPT");
});

function fakeAccessToken(session: string): string {
  const payload = JSON.stringify({ "https://api.openai.com/auth": { chatgpt_account_id: "test-account" }, session });
  return `header.${Buffer.from(payload).toString("base64url")}.signature`;
}
