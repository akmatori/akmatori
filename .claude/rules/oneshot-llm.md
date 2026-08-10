---
paths:
  - "**/internal/services/**"
  - "**/internal/alerts/extraction/**"
  - "**/agent-worker/src/oneshot-llm.ts"
---

# One-shot LLM path

Use the one-shot path for short non-agent calls such as: incident title generation,
free-form alert extraction, Slack final-message summarization, response formatting,
feedback classification, alert correlation.

- API frame type is `oneshot_llm_request`; worker replies with `oneshot_llm_response`
- Go callers depend on `services.OneShotLLMCaller`, not concrete worker code
- If the worker is disconnected, callers must fail gracefully and use deterministic fallbacks
- `oneshot-llm.ts` retries once without `temperature` when a provider rejects it, then caches
  that model key for the worker lifetime
- If a feature only needs a single completion, do not spin up a full agent session

Key files: `internal/services/title_generator.go`, `internal/services/slack_summarizer.go`,
`internal/services/feedback_classifier.go`, `agent-worker/src/oneshot-llm.ts`
