/**
 * Proxy configuration helpers shared between agent sessions and one-shot LLM
 * calls. Sets undici's global dispatcher and HTTP_PROXY env vars when the
 * caller-side LLM proxy toggle is enabled, otherwise resets to no-proxy.
 *
 * The global dispatcher is process-wide state; concurrent callers receive
 * the same setting in practice because proxy config is system-global.
 */

import { Agent as UndiciAgent, EnvHttpProxyAgent, setGlobalDispatcher } from "undici";
import type { ProxyConfig } from "./types.js";

export function applyProxyConfig(proxyConfig: ProxyConfig | undefined): void {
  if (proxyConfig?.url && proxyConfig.llm_enabled) {
    process.env.HTTP_PROXY = proxyConfig.url;
    process.env.HTTPS_PROXY = proxyConfig.url;
    process.env.NO_PROXY = proxyConfig.no_proxy || "";

    setGlobalDispatcher(new EnvHttpProxyAgent());
  } else {
    process.env.HTTP_PROXY = "";
    process.env.HTTPS_PROXY = "";
    process.env.NO_PROXY = "";

    setGlobalDispatcher(new UndiciAgent());
  }
}
