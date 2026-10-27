---
paths:
  - "**/internal/services/**"
  - "**/internal/alerts/extraction/**"
  - "**/agent-worker/src/oneshot-llm.ts"
---

# One-shot LLM path

Use the one-shot path for short non-agent calls such as: incident title generation,
free-form alert extraction, Slack final-message summarization, response formatting,
feedback classification, alert correlation, event classification (webhook items get their own
call; free-text sources get the class fields inside the extraction call).

- API frame type is `oneshot_llm_request`; worker replies with `oneshot_llm_response`
- Go callers depend on `services.OneShotLLMCaller`, not concrete worker code
- If the worker is disconnected, callers must fail gracefully and use deterministic fallbacks
- `oneshot-llm.ts` retries once without `temperature` when a provider rejects it, then caches
  that model key for the worker lifetime
- `maxTokens` from Go callers is the ANSWER budget. For a model with `reasoning: true` (all
  synthesized custom specs) the worker adds `REASONING_HEADROOM_TOKENS` (4096), because providers
  count reasoning against the same limit; an empty answer with `stopReason: "length"` throws, so the
  Go caller logs an error instead of reading "" as a real answer
- If a feature only needs a single completion, do not spin up a full agent session

Key files: `internal/services/title_generator.go`, `internal/services/slack_summarizer.go`,
`internal/services/feedback_classifier.go`, `agent-worker/src/oneshot-llm.ts`
