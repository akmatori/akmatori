# Subscription connections

Akmatori supports API-key connections and a Codex subscription connection.
Subscription limits are separate from API billing credits. Available models,
quotas, and eligibility depend on the account and provider.

| Connection | Support in Akmatori |
| --- | --- |
| OpenAI, Anthropic, Google, and other existing APIs | Existing API-key configuration |
| Codex with an eligible ChatGPT subscription | Worker-side OAuth through the bundled pi runtime |
| Claude Code subscription | Not implemented; requires the unmodified official Claude Code client |
| Gemini CLI with Google AI Pro/Ultra | Not implemented; the pinned pi SDK removed this provider |

Claude Code and Gemini CLI require separate execution integrations that preserve
Akmatori's gateway authorization, approval flow, cancellation, and child agents.
Do not paste their session tokens into an API-key field. These follow-ups are
tracked in [issue #29](https://github.com/akmatori/akmatori/issues/29).

## Connect Codex

1. Deploy the updated API, worker, and frontend together.
2. Open an interactive session as the worker's normal user:

   ```sh
   docker compose exec akmatori-agent pi
   ```

3. Enter `/login` and select the Codex provider (`openai-codex`, displayed as
   OpenAI Codex / ChatGPT in the bundled SDK). For a remote Docker host, choose
   device-code login and complete sign-in in your own browser. Account settings
   may require enabling device-code authentication. Do not publish a callback
   port or paste credentials into Akmatori.
4. In **Settings → LLM Provider**, add a **Codex (subscription)** configuration,
   select a model, save it, and activate it. Existing seeded configurations can
   also be edited and activated. Use models supported by the worker catalog:

   ```sh
   docker compose exec akmatori-agent pi --list-models openai-codex
   ```

5. Check authentication without printing credentials:

   ```sh
   docker compose exec akmatori-agent pi auth check --provider openai-codex
   ```

Saving/activating a configuration validates its shape; it does **not** verify the
live worker login. Authentication is checked when the worker handles a request.
Investigations, child agents, and one-shot tasks use the worker's stored OAuth
credentials. The SDK refreshes them using its credential-store locking.

The bundled SDK, rather than Akmatori, owns the sign-in flow. Its provider support
can change; consult the installed catalog and upstream release notes when
upgrading. A subscription model missing from the worker catalog fails explicitly
instead of being routed to an API endpoint.

## Storage and disconnection

Credentials remain in the pi auth store under `/home/agent/.pi/agent` on the
existing `agent_sessions` volume. They are not sent to the browser or stored in
PostgreSQL. Protect this volume and its backups with encryption at rest and
restrictive permissions. The worker and its tools are trusted processes sharing
that operating-system identity; this is not isolation between tenants.

One subscription account per provider is shared by this worker and its child
agents. Multiple named configurations select models, not separate accounts.
To disconnect, open `pi` in the worker and use `/logout` for the Codex provider.
Deleting an LLM configuration does not revoke the shared account credentials.

If login is missing, revoked, or cannot refresh, the request fails with sign-in
instructions. Internal one-shot features retain their existing deterministic
fallbacks. Akmatori never switches a subscription request to paid API-key access.
Reauthenticate in the worker after account revocation or expiry.

Token counts remain available. `cost_usd` is zero for subscription investigations:
it does not estimate the plan fee, remaining quota, or provider-billed overages.

## API configuration

Create a subscription configuration using the existing authenticated settings
API, then activate its returned ID:

```http
POST /api/settings/llm
Content-Type: application/json

{"name":"Codex subscription","provider":"openai-codex","model":"gpt-5.5"}
```

```http
PUT /api/settings/llm/{id}/activate
```

`api_key` and `base_url` must be omitted or empty. A nonblank model is required.
The same restrictions apply to updates. `uses_subscription` identifies the
connection type; `is_configured` describes configuration validity, not live login
status. Existing API-key providers retain their behavior.

## Provider references

- [Codex authentication](https://developers.openai.com/codex/auth)
- [Claude Code credential use](https://code.claude.com/docs/en/legal-and-compliance)
- [Gemini CLI authentication and subscription accounts](https://geminicli.com/docs/get-started/authentication/)
