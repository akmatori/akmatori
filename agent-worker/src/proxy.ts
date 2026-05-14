/**
 * Proxy configuration helpers shared between agent sessions and one-shot LLM
 * calls. Sets undici's global dispatcher and proxy env vars when the
 * caller-side LLM proxy toggle is enabled, otherwise resets to the container's
 * initial env (which docker-compose populates from .env HTTP_PROXY).
 *
 * The global dispatcher is process-wide state; concurrent callers receive
 * the same setting in practice because proxy config is system-global.
 */

import { Agent as UndiciAgent, EnvHttpProxyAgent, setGlobalDispatcher } from "undici";
import type { ProxyConfig } from "./types.js";

// undici's EnvHttpProxyAgent uses `??` (not `||`) when reading env vars, so an
// empty lowercase `http_proxy=""` silently shadows a populated uppercase
// `HTTP_PROXY=...`. docker-compose injects both cases (defaulting the unset
// one to `""`), so we resolve initial values here once and pass explicit
// options into EnvHttpProxyAgent below.
function resolveInitial(upperName: string, lowerName: string): string {
  const upper = process.env[upperName];
  if (upper) return upper;
  const lower = process.env[lowerName];
  if (lower) return lower;
  return "";
}

// Snapshot the container's initial proxy env at module load so we can restore
// it when no DB-configured proxy URL is present. Without this snapshot
// applyProxyConfig would silently clobber the operator-level proxy before
// every LLM call when the operator has not configured a proxy URL in the web
// UI.
const initialHttpProxy = resolveInitial("HTTP_PROXY", "http_proxy");
const initialHttpsProxy = resolveInitial("HTTPS_PROXY", "https_proxy");
const initialNoProxy = resolveInitial("NO_PROXY", "no_proxy");

function syncProxyEnv(httpProxy: string, httpsProxy: string, noProxy: string): void {
  // Sync both upper- and lower-case so bash subprocesses and any library
  // that reads only one case agree with undici on the active proxy.
  process.env.HTTP_PROXY = httpProxy;
  process.env.HTTPS_PROXY = httpsProxy;
  process.env.NO_PROXY = noProxy;
  process.env.http_proxy = httpProxy;
  process.env.https_proxy = httpsProxy;
  process.env.no_proxy = noProxy;
}

export function applyProxyConfig(proxyConfig: ProxyConfig | undefined): void {
  if (proxyConfig?.url && proxyConfig.llm_enabled) {
    const noProxy = proxyConfig.no_proxy || "";
    syncProxyEnv(proxyConfig.url, proxyConfig.url, noProxy);
    setGlobalDispatcher(
      new EnvHttpProxyAgent({
        httpProxy: proxyConfig.url,
        httpsProxy: proxyConfig.url,
        noProxy,
      }),
    );
    return;
  }

  if (proxyConfig?.url && !proxyConfig.llm_enabled) {
    // Operator has a DB-configured proxy but explicitly disabled it for LLM
    // traffic — honor that decision and bypass any container-env proxy too.
    syncProxyEnv("", "", "");
    setGlobalDispatcher(new UndiciAgent());
    return;
  }

  // No DB-configured proxy URL: restore the container's initial env so
  // operator-level HTTP_PROXY from .env still reaches LLM calls.
  syncProxyEnv(initialHttpProxy, initialHttpsProxy, initialNoProxy);
  if (initialHttpProxy || initialHttpsProxy) {
    setGlobalDispatcher(
      new EnvHttpProxyAgent({
        httpProxy: initialHttpProxy,
        httpsProxy: initialHttpsProxy,
        noProxy: initialNoProxy,
      }),
    );
  } else {
    setGlobalDispatcher(new UndiciAgent());
  }
}
