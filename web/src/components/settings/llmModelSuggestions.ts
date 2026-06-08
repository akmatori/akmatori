import type { LLMProvider } from '../../types';

export const MODEL_SUGGESTIONS: Record<LLMProvider, { value: string; label: string }[]> = {
  openai: [
    { value: 'gpt-5.5', label: 'gpt-5.5 (Recommended)' },
    { value: 'gpt-5.5-pro', label: 'gpt-5.5-pro (Most capable)' },
    { value: 'gpt-5.4', label: 'gpt-5.4' },
    { value: 'gpt-5.4-mini', label: 'gpt-5.4-mini (Fast)' },
    { value: 'gpt-5.3-codex', label: 'gpt-5.3-codex' },
    { value: 'gpt-5-mini', label: 'gpt-5-mini (Budget)' },
    { value: 'o4-mini', label: 'o4-mini (Reasoning)' },
  ],
  anthropic: [
    { value: 'claude-opus-4-8', label: 'claude-opus-4-8 (Most capable)' },
    { value: 'claude-opus-4-7', label: 'claude-opus-4-7' },
    { value: 'claude-opus-4-6', label: 'claude-opus-4-6' },
    { value: 'claude-sonnet-4-6', label: 'claude-sonnet-4-6 (Recommended)' },
    { value: 'claude-sonnet-4-5', label: 'claude-sonnet-4-5' },
    { value: 'claude-haiku-4-5', label: 'claude-haiku-4-5 (Fast)' },
  ],
  google: [
    { value: 'gemini-3-pro-preview', label: 'gemini-3-pro-preview (Recommended)' },
    { value: 'gemini-3.1-pro-preview', label: 'gemini-3.1-pro-preview (Preview)' },
    { value: 'gemini-3-flash-preview', label: 'gemini-3-flash-preview (Fast)' },
    { value: 'gemini-2.5-pro', label: 'gemini-2.5-pro' },
    { value: 'gemini-2.5-flash', label: 'gemini-2.5-flash' },
    { value: 'gemini-2.0-flash', label: 'gemini-2.0-flash (Stable)' },
  ],
  openrouter: [
    { value: 'anthropic/claude-opus-4.8', label: 'anthropic/claude-opus-4.8 (Most capable)' },
    { value: 'anthropic/claude-opus-4.7', label: 'anthropic/claude-opus-4.7' },
    { value: 'openai/gpt-5.5', label: 'openai/gpt-5.5 (Recommended)' },
    { value: 'google/gemini-3.1-pro-preview', label: 'google/gemini-3.1-pro-preview' },
    { value: 'anthropic/claude-sonnet-4.6', label: 'anthropic/claude-sonnet-4.6' },
    { value: 'openai/gpt-5.4', label: 'openai/gpt-5.4' },
    { value: 'openai/gpt-5.4-mini', label: 'openai/gpt-5.4-mini' },
    { value: 'google/gemini-2.5-pro', label: 'google/gemini-2.5-pro' },
  ],
  nvidia: [
    { value: 'meta/llama-3.3-70b-instruct', label: 'meta/llama-3.3-70b-instruct (Recommended)' },
    { value: 'meta/llama-3.1-70b-instruct', label: 'meta/llama-3.1-70b-instruct' },
    { value: 'nvidia/nemotron-3-super-120b-a12b', label: 'nvidia/nemotron-3-super-120b-a12b (Most capable)' },
    { value: 'nvidia/nemotron-3-nano-30b-a3b', label: 'nvidia/nemotron-3-nano-30b-a3b (Fast)' },
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
