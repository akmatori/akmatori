# Subscription authentication

## Goal

Allow operators to use eligible subscription access alongside existing API-key
configurations, without treating subscription quotas as API credit balances.
The requested providers are Codex, Claude Code, and Gemini CLI (Google AI Pro).

## Architecture findings

- Go stores named LLM configurations and forwards the active choice to the worker.
- The TypeScript worker uses pi for investigations, child agents, and one-shot calls.
- The pinned pi SDK supports Codex OAuth and coordinated credential refresh.
- The pinned SDK no longer includes the Gemini CLI provider.
- Claude subscription credentials must remain inside the unmodified official
  Claude Code client; a third-party OAuth adapter is not appropriate.

## Implementation

1. Add subscription-aware configuration and validation without changing API-key
   behavior. Keep credentials in the worker's existing persistent pi auth store.
2. Resolve subscription authentication for investigations and one-shot requests;
   children use the same credential store. Fail closed when sign-in is absent.
3. Expose the supported connection options and operator setup instructions.
4. Cover configuration, authentication failures, model routing, and compatibility
   with tests; run `make verify`, builds, and the frontend linter.
5. Document provider limitations and any separate official-client integration.

## Security

- Data: subscription access/refresh tokens are restricted secrets; configuration
  names and model identifiers are internal metadata.
- Access: deployment operators authenticate the worker through the provider's
  sign-in flow. Existing settings authorization remains applicable.
- Threats: token disclosure, sending OAuth tokens to custom URLs, silent API
  billing fallback, expired sessions, and concurrent token refresh.
- Controls: no subscription tokens in REST payloads, PostgreSQL, issue bodies,
  or logs; subscription providers reject API keys and custom endpoints; use
  the SDK's credential locking and refresh; missing login fails explicitly.
- Storage: the existing pi auth store requires a protected, encrypted deployment
  volume and restrictive filesystem permissions. It is not a tenant boundary;
  this work must not imply isolation from trusted worker tools or host operators.
- No production accounts, paid inference, or production deployments are used for
  validation. Live subscription verification needs an operator-owned account.

## Contribution

Use an English issue and pull request from a contributor fork. Commits use
Conventional Commits without scope or generated-by/co-author trailers. Do not
merge or deploy as part of this change.

## Outcome

The first contribution implements Codex subscription access across configuration,
investigations, child-agent configuration, and one-shot tasks. No schema migration
or new package is required. Claude Code and Gemini CLI need separate official-client
execution integrations and remain tracked in issue #29. Validation and remaining
checks are recorded in `docs/security/scans/2026-10-03-subscription-auth.md`.
