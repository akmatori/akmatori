import type { LLMProvider } from '../../types';

export const MODEL_SUGGESTIONS: Record<LLMProvider, { value: string; label: string }[]> = {
  'openai-codex': [
    { value: 'gpt-5.6-terra', label: 'gpt-5.6-terra (Recommended)' },
    { value: 'gpt-5.5', label: 'gpt-5.5' },
    { value: 'gpt-6.1-sol', label: 'gpt-6.1-sol' },
  ],
  openai: [
    // GPT-6 line (pi-ai 1.0.1 OpenAI catalog): gpt-6.1-sol, gpt-6-sol,
    // gpt-6-luna, gpt-6-astra. There is no plain `gpt-6` id, and the
    // `gpt-6-astra-pro` / `-fast` ids exist only in the OpenRouter and Vercel
    // AI Gateway catalogs, NOT at OpenAI — do not list them here. Not marked
    // Recommended until they have run through the bench; gpt-5.6-terra stays
    // the known-good default.
    { value: 'gpt-6.1-sol', label: 'gpt-6.1-sol' },
    { value: 'gpt-6-sol', label: 'gpt-6-sol' },
    { value: 'gpt-6-astra', label: 'gpt-6-astra' },
    { value: 'gpt-6-luna', label: 'gpt-6-luna (Budget)' },
    // gpt-5.6 ships as three named variants; there is no plain `gpt-5.6` id.
    // Ordering follows their catalogue pricing (sol > terra > luna), the only
    // capability signal the model catalog exposes.
    { value: 'gpt-5.6-terra', label: 'gpt-5.6-terra (Recommended)' },
    { value: 'gpt-5.6-sol', label: 'gpt-5.6-sol' },
    { value: 'gpt-5.6-luna', label: 'gpt-5.6-luna (Budget)' },
    { value: 'gpt-5.5', label: 'gpt-5.5' },
    { value: 'gpt-5.5-pro', label: 'gpt-5.5-pro' },
    { value: 'gpt-5.4', label: 'gpt-5.4' },
    { value: 'gpt-5.4-mini', label: 'gpt-5.4-mini (Fast)' },
    { value: 'gpt-5.3-codex', label: 'gpt-5.3-codex' },
    { value: 'gpt-5-mini', label: 'gpt-5-mini (Budget)' },
    { value: 'o4-mini', label: 'o4-mini (Reasoning)' },
  ],
  anthropic: [
    // pi-ai 1.0.1 catalog. Opus 5.5 / Sonnet 5.5 carry a 1M context window
    // and adaptive thinking.
    { value: 'claude-fable-5-1', label: 'claude-fable-5-1 (Most capable)' },
    { value: 'claude-fable-5', label: 'claude-fable-5' },
    { value: 'claude-opus-5-5', label: 'claude-opus-5-5' },
    { value: 'claude-sonnet-5-5', label: 'claude-sonnet-5-5' },
    { value: 'claude-opus-5', label: 'claude-opus-5' },
    { value: 'claude-sonnet-5', label: 'claude-sonnet-5 (Recommended)' },
    { value: 'claude-opus-4-8', label: 'claude-opus-4-8' },
    { value: 'claude-opus-4-7', label: 'claude-opus-4-7' },
    { value: 'claude-sonnet-4-6', label: 'claude-sonnet-4-6' },
    { value: 'claude-haiku-4-5', label: 'claude-haiku-4-5 (Fast)' },
  ],
  google: [
    // pi-ai 1.0.1 catalog no longer lists gemini-3-pro-preview or
    // gemini-2.0-flash; they still resolve through the synthesized spec
    // (cost reported as 0) and are kept for existing configs.
    { value: 'gemini-3.1-pro-preview', label: 'gemini-3.1-pro-preview (Recommended)' },
    { value: 'gemini-3.8-flash', label: 'gemini-3.8-flash (Fast)' },
    { value: 'gemini-3.7-flash', label: 'gemini-3.7-flash' },
    { value: 'gemini-3.6-flash', label: 'gemini-3.6-flash' },
    { value: 'gemini-3.5-flash', label: 'gemini-3.5-flash' },
    { value: 'gemini-3.1-flash-lite', label: 'gemini-3.1-flash-lite (Budget)' },
    { value: 'gemini-3-pro-preview', label: 'gemini-3-pro-preview' },
    { value: 'gemini-3-flash-preview', label: 'gemini-3-flash-preview' },
    { value: 'gemini-2.5-pro', label: 'gemini-2.5-pro' },
    { value: 'gemini-2.5-flash', label: 'gemini-2.5-flash' },
    { value: 'gemini-2.0-flash', label: 'gemini-2.0-flash' },
  ],
  openrouter: [
    { value: 'anthropic/claude-fable-5', label: 'anthropic/claude-fable-5 (Most capable)' },
    { value: 'anthropic/claude-sonnet-5', label: 'anthropic/claude-sonnet-5' },
    { value: 'anthropic/claude-opus-4.8', label: 'anthropic/claude-opus-4.8' },
    // OpenRouter lists gpt-6-astra and -pro only (no -fast); ids checked 2026-09-20.
    { value: 'openai/gpt-6-astra', label: 'openai/gpt-6-astra' },
    { value: 'openai/gpt-6-astra-pro', label: 'openai/gpt-6-astra-pro' },
    { value: 'openai/gpt-5.6-terra', label: 'openai/gpt-5.6-terra (Recommended)' },
    { value: 'openai/gpt-5.6-sol', label: 'openai/gpt-5.6-sol' },
    { value: 'openai/gpt-5.6-luna', label: 'openai/gpt-5.6-luna (Budget)' },
    { value: 'google/gemini-3.1-pro-preview', label: 'google/gemini-3.1-pro-preview' },
    { value: 'anthropic/claude-sonnet-4.6', label: 'anthropic/claude-sonnet-4.6' },
    { value: 'openai/gpt-5.5', label: 'openai/gpt-5.5' },
    { value: 'openai/gpt-5.4', label: 'openai/gpt-5.4' },
    { value: 'openai/gpt-5.4-mini', label: 'openai/gpt-5.4-mini' },
    { value: 'google/gemini-2.5-pro', label: 'google/gemini-2.5-pro' },
  ],
  nvidia: [
    { value: 'meta/llama-3.3-70b-instruct', label: 'meta/llama-3.3-70b-instruct (Recommended)' },
    { value: 'meta/llama-3.1-70b-instruct', label: 'meta/llama-3.1-70b-instruct' },
    // nemotron-3-super-120b-a12b was removed: NVIDIA no longer serves it
    // (pi 1.0.1 changelog). The ids below are pi-ai 1.0.1's NIM catalog;
    // the Llama entries above are no longer in it but still resolve via the
    // synthesized spec (cost reported as 0).
    { value: 'nvidia/nemotron-3-ultra-550b-a55b', label: 'nvidia/nemotron-3-ultra-550b-a55b (Most capable)' },
    { value: 'nvidia/nemotron-3.5-lightning-30b-a3b', label: 'nvidia/nemotron-3.5-lightning-30b-a3b (Fast)' },
    { value: 'nvidia/nemotron-3-nano-omni-30b-a3b-reasoning', label: 'nvidia/nemotron-3-nano-omni-30b-a3b-reasoning' },
    { value: 'nvidia/llama-3.1-nemotron-70b-instruct', label: 'nvidia/llama-3.1-nemotron-70b-instruct' },
    { value: 'nvidia/nemotron-3-nano-30b-a3b', label: 'nvidia/nemotron-3-nano-30b-a3b' },
  ],
  minimax: [
    { value: 'MiniMax-M3', label: 'MiniMax-M3 (Recommended)' },
    { value: 'MiniMax-M2.7', label: 'MiniMax-M2.7' },
    { value: 'MiniMax-M2.7-highspeed', label: 'MiniMax-M2.7-highspeed (Fast)' },
  ],
  'ant-ling': [
    { value: 'Ling-2.6-1T', label: 'Ling-2.6-1T (Recommended)' },
    { value: 'Ling-2.6-flash', label: 'Ling-2.6-flash (Fast)' },
    { value: 'Ring-2.6-1T', label: 'Ring-2.6-1T' },
  ],
  custom: [],
};
