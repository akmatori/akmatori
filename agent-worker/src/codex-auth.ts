import { join } from "node:path";
import { getAgentDir, ModelRuntime } from "@earendil-works/pi-coding-agent";
import type { Model } from "@earendil-works/pi-ai";
import type { CodexAuthAction, CodexAuthStatus, ProxyConfig } from "./types.js";
import { applyProxyConfig } from "./proxy.js";

export const CODEX_PROVIDER = "openai-codex";
export const CODEX_DEFAULT_MODEL = "gpt-6.1-sol";

export function createCodexRuntime(refreshOnCreate = false): Promise<ModelRuntime> {
  return ModelRuntime.create({
    authPath: join(getAgentDir(), "akmatori-codex-auth.json"),
    modelsPath: null,
    allowModelNetwork: false,
    refreshOnCreate,
  });
}

export async function resolveCodexAccessToken(
  runtime: ModelRuntime,
  model: Model<any>,
  signal?: AbortSignal,
): Promise<string> {
  const timeout = AbortSignal.timeout(15_000);
  let apiKey: string | undefined;
  try {
    apiKey = (await runtime.getAuth(model, {
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    }))?.auth.apiKey;
  } catch {
    signal?.throwIfAborted();
    throw new Error("Could not refresh Codex authorization. Reconnect ChatGPT in LLM settings.");
  }
  if (!apiKey) {
    throw new Error("Connect ChatGPT in LLM settings to use the Codex subscription.");
  }
  return apiKey;
}

/** One device authorization at a time for the worker's shared Codex account. */
export class CodexAuth {
  private pending?: { controller: AbortController; done?: Promise<void>; user_code?: string };
  private error?: string;
  private disconnecting?: Promise<void>;

  async handle(action: CodexAuthAction, proxyConfig?: ProxyConfig): Promise<CodexAuthStatus> {
    if (action === "start") {
      if (!this.pending && !this.disconnecting) {
        this.error = undefined;
        const pending: NonNullable<CodexAuth["pending"]> = { controller: new AbortController() };
        this.pending = pending;
        pending.done = this.login(pending, proxyConfig);
      }
      return this.status();
    }
    if (action === "disconnect") {
      this.disconnecting ??= this.disconnect().finally(() => { this.disconnecting = undefined; });
      await this.disconnecting;
    }
    if (action === "cancel") {
      await this.cancel();
    }
    return this.status();
  }

  private async status(): Promise<CodexAuthStatus> {
    const runtime = await createCodexRuntime();
    const credentials = await runtime.listCredentials();
    return {
      connected: credentials.some((entry) => entry.providerId === CODEX_PROVIDER && entry.type === "oauth"),
      pending: this.pending !== undefined,
      user_code: this.pending?.user_code,
      error: this.error,
    };
  }

  private async login(
    pending: NonNullable<CodexAuth["pending"]>,
    proxyConfig?: ProxyConfig,
  ): Promise<void> {
    const { controller } = pending;
    const timedOut = new Error("Codex sign-in expired. Request a new device code.");
    const timer = setTimeout(() => controller.abort(timedOut), 16 * 60_000);
    timer.unref();
    try {
      applyProxyConfig(proxyConfig);
      const runtime = await createCodexRuntime();
      await runtime.login(CODEX_PROVIDER, "oauth", {
        signal: controller.signal,
        async prompt(prompt) {
          if (prompt.type === "select" && prompt.options.some(({ id }) => id === "device_code")) {
            return "device_code";
          }
          throw new Error("Codex device sign-in is unavailable");
        },
        notify(event) {
          if (event.type === "device_code") pending.user_code = event.userCode;
        },
      });
    } catch (err) {
      // Credential synchronization can fail after a successful SDK write.
      if ((err as Error)?.name !== "CredentialSynchronizationError") {
        if (controller.signal.reason === timedOut) {
          this.error = timedOut.message;
        } else if (!controller.signal.aborted) {
          this.error = "Codex sign-in failed. Enable device code authorization in ChatGPT security settings and try again.";
        }
      }
    } finally {
      clearTimeout(timer);
      this.pending = undefined;
    }
  }

  async cancel(): Promise<void> {
    this.pending?.controller.abort();
    await this.pending?.done;
    this.error = undefined;
  }

  private async disconnect(): Promise<void> {
    await this.cancel();
    const runtime = await createCodexRuntime();
    try {
      await runtime.logout(CODEX_PROVIDER);
    } catch (err) {
      if ((err as Error)?.name !== "CredentialSynchronizationError") throw err;
    }
  }
}
