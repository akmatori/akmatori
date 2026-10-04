import type { ModelRuntime } from "@earendil-works/pi-coding-agent";
import type { LLMSettings } from "./types.js";

export function usesSubscription(provider: string): boolean {
  return provider === "openai-codex";
}

export function validateSubscriptionSettings(settings: LLMSettings): void {
  if (!usesSubscription(settings.provider)) return;
  if (settings.api_key || settings.base_url || !settings.model.trim()) {
    throw new Error("Subscription configurations require a model and do not accept API keys or custom endpoints");
  }
}

/** Require stored OAuth credentials; never fall back to an ambient API key. */
export async function requireSubscriptionLogin(
  runtime: Pick<ModelRuntime, "listCredentials" | "getAuth">,
  provider: string,
  signal?: AbortSignal,
): Promise<void> {
  const credentials = await runtime.listCredentials({ signal });
  if (!credentials.some((credential) => credential.providerId === provider && credential.type === "oauth")) {
    throw new Error(`Subscription sign-in required for ${provider}. Run pi in the agent container and use /login.`);
  }
  try {
    if (!(await runtime.getAuth(provider, { signal }))) throw new Error("No authentication");
  } catch {
    // Refresh errors can contain provider response bodies. Keep tokens out of
    // the worker WebSocket, logs, and incident history.
    throw new Error(`Subscription authentication failed for ${provider}. Sign in again in the agent container.`);
  }
}
