---
paths:
  - "**/internal/services/**"
  - "**/internal/alerts/extraction/**"
  - "**/agent-worker/src/oneshot-llm.ts"
---

# One-shot LLM path

Use the one-shot path for short non-agent calls such as: incident title generation,
free-form alert extraction, Slack final-message summarization, response formatting,
feedback classification, operator text → memory draft, alert correlation, event
classification (webhook items get their own call; free-text sources get the class fields
inside the extraction call).

- API frame type is `oneshot_llm_request`; worker replies with `oneshot_llm_response`
- Go callers depend on `services.OneShotLLMCaller`, not concrete worker code
- If the worker is disconnected, callers must fail gracefully and use deterministic fallbacks
- `oneshot-llm.ts` retries once without `temperature` when a provider rejects it, then caches
  that model key for the worker lifetime
- `maxTokens` from Go callers is the ANSWER budget. For a model with `reasoning: true` (all
  synthesized custom specs) the worker adds `REASONING_HEADROOM_TOKENS` (4096), because providers
  count reasoning against the same limit; an empty answer with `stopReason: "length"` throws, so the
  Go caller logs an error instead of reading "" as a real answer
- synthesized `custom` specs set `compat.supportsDeveloperRole: false` (parent spec AND child
  `models.json`): pi-ai sends the system prompt as role `developer` for a reasoning model on an
  unknown URL, and gateways can drop that role silently — gcore's DeepSeek endpoint did, so every
  one-shot call ran with no instructions (prose instead of JSON, 30-40 s per call)
- time limits: Go callers own the deadline (correlation and merge 45 s: a reasoning model needs
  13-20 s for a 25-candidate prompt); the worker HTTP limit (60 s) stays above all of them. A
  generated title on several lines or over 120 runes is rejected for the fallback
- If a feature only needs a single completion, do not spin up a full agent session

Key files: `internal/services/title_generator.go`, `internal/services/slack_summarizer.go`,
`internal/services/feedback_classifier.go`, `agent-worker/src/oneshot-llm.ts`
