---
paths:
  - "**/mcp-gateway/**"
  - "**/agent-worker/src/gateway-tools.ts"
  - "**/internal/services/git_repo_sync*.go"
---

# MCP Gateway and tool routing

Flow: agent reads its skill's `SKILL.md` and the generated `connected-tools/SKILL.md` (every
connected tool; see the `tool-authorization` rule) → agent calls
`gateway_call(toolName, args, instanceHint?)` → worker sends JSON-RPC to MCP Gateway with
`X-Incident-ID` (the run's scope: incident UUID or request scope, see the `agent-runs` rule) and
`X-Run-ID` (the run; `mcp.RunIDFromContext` in handlers) →
gateway resolves routing, enforces allowlists, executes, returns output.

- do not teach agents to call tool implementations directly; routing is handled by logical instance
  names or instance hints
- `ToolAllowlist` on `WebSocketMessage` is intentionally NOT `omitempty` — `[]` means reject-all,
  `null` means allow-all
- MCP proxy tools (`<namespace_prefix>.<tool>`) go through the allowlist like every tool: the
  tool type is the namespace prefix, and the entry comes from the MCP server's managed tool
  instance (see the `connectors` rule). There is no proxy-namespace bypass any more; `tools/list`
  is filtered by the allowlist too. `logical_name` / `tool_instance_id` are stripped before a call
  goes upstream. Upstream names with `.` or other characters are sanitized to `_` in the
  namespaced name (`disabled_tools` and status use the original names)
- remote MCP only (no stdio). Outbound MCP HTTP uses `NetPolicy` (`netpolicy.go`): the resolved IP
  is checked at dial time; loopback, link-local (incl. 169.254.169.254), unspecified and multicast
  are refused; private ranges are allowed unless `MCP_BLOCK_PRIVATE_NETWORKS=true`;
  `MCP_ALLOW_LOOPBACK=true` for dev/tests; no redirects; no env proxy (it would hide the
  destination); 10 MiB response cap. Streamable HTTP responses may be JSON or `text/event-stream`
- reload closes and re-initializes only servers whose url/transport/auth/namespace/disabled_tools
  changed, and closes removed ones. Reload records the wanted config per server (`pool.Expect`);
  a connect that finishes with another config, or for a removed server, is dropped with
  `ErrStaleConfig` instead of replacing the current connection
- a stored-server test (`/mcp-servers/test` with `id`) starts from the stored config and applies
  each given field (url, auth_config, transport) on its own
- `/registry/tools?namespace=<ns>` (internal) lists a namespace's registered tools with input
  schemas; the API renders them into `connected-tools/SKILL.md`
- admin endpoints (`/reload/*`, `/mcp-servers/test`, `/mcp-servers/status`, `/registry/tools`) are `internalOnly`:
  requests with forwarding headers are refused. The edge proxy (`proxy/nginx.conf`) exposes only
  `/mcp/tools*` (schemas for the UI); `/mcp/mcp` and the admin paths are internal

## Incidents tool (built-in, credential-less)

The `incidents` tool exposes `incidents.list` and `incidents.get` for read-only access to Akmatori's
own incident records. It is the only built-in tool that queries the gateway's own DB connection
(`database.DB`) directly rather than proxying to an external service.

- `EnsureToolTypes()` seeds both the `ToolType` and a single `ToolInstance` (logical name
  `"incidents"`, Name `"Incidents"`, empty Settings) so the tool appears in all pickers with zero
  operator configuration
- the seeded instance never requires credentials — do not add auth fields to it
- registered in `registry.go` via `registerIncidentsTools()` with no rate limiter
- `incidents` is in `builtInToolNamespaces`; the auth allowlist entry shape is
  `{ToolType: "incidents"}` (no InstanceID/LogicalName)
- `List` returns summary fields only (tokens summed over the runs); `Get` folds the incident's
  `agent_runs` (mirror model `AgentRun`, `database.LoadRunsWork`): newest `response`, every run's log
  keeping the newest 50,000 bytes
- when adding another credential-less built-in tool, follow the same seed pattern in
  `EnsureToolTypes()` and the same `registerXxxTools()` pattern in `registry.go`

`docs/TOOL_ARCHITECTURE.md` still describes the retired Python tool path; live routing is
gateway-first via `gateway_call`.

## Events tool (built-in, credential-less)

`events.search` / `events.get` read the `events` table; `events.report_alert` lets a CRON run
report a problem. Same seed and registration pattern as `incidents`; `events` is in
`builtInToolNamespaces` and `credentiallessNamespaces`.

- incident runs always get it; cron runs get it through `builtin_tools` (default on): a type-only
  `{ToolType: "events"}` entry (`IncidentRunAllowlist`, `CronAllowlist`)
- `get` on a request event adds `run` (the scope's answer and log)
- `report_alert` refuses non-cron runs (it looks up the newest run of the `X-Incident-ID` scope and
  takes the job name from `cron_jobs`), caps reports
  at 20 per run, and only INSERTS an event with `pending_dispatch=true`: the gateway cannot reach the
  API, so the API's `ReportedEventDispatcher` (5 s poll, conditional-update claim) runs it through
  `ProcessReportedAlert`
- `get` cuts the raw payload to 20,000 bytes of JSON (returned as a string with
  `raw_payload_truncated: true`)

## Knowledge tools (built-in, credential-less): runbooks.*, memory.*

`runbooks.search` / `runbooks.get` and `memory.search` / `memory.list` / `memory.get` /
`memory.write` / `memory.delete` (`tools/knowledge`, `registerKnowledgeTools`). They replaced the
pi-subagents; behaviour and memory rules are in the `memory-and-runbooks` rule.

- `runbooks` and `memory` are in `builtInToolNamespaces`, `credentiallessNamespaces` and the API's
  `reservedToolNamespaces`; incident runs and proposal chat get both, crons via `builtin_tools`
- reads need no run id; writes need `X-Run-ID` of a running run of the `X-Incident-ID` scope that
  is not proposal chat (`requireRun`), caps are per run id (in memory, a restart resets them)
- writes go through the gateway's own `database.DB`; the API sees them at once and refreshes
  SKILL.md manifests in `FinishRun`

## Telegram tool (connector-backed, no tool instance): telegram.*

`telegram.list_chats` / `telegram.send_message` (`tools/telegram`, `registerTelegramTools`). The
gateway reads the bot token and chats from the Telegram connector's `integrations` / `channels`
rows (mirror models in `database/models_messaging.go`); nothing is configured twice.

- only enabled, `can_post` chats of an enabled Telegram integration with a `bot_token` are
  accepted, by chat id or name; without `chat` the default chat (or the only chat) is used
- plain text (no `parse_mode`), split at 4096 runes, at most 4 parts; `chat:topic` external ids
  post to the forum topic; one retry on 429; errors never carry the request URL (it holds the token)
- sends need `X-Run-ID` of a running, non-proposal run of the scope; at most 10 sends per run
  (in memory); a failed send is free. The HTTP proxy follows the Slack proxy toggle, like the API
- `telegram` is in `builtInToolNamespaces`, `credentiallessNamespaces` and the API's
  `reservedToolNamespaces`; the allowlist entry comes from `messagingToolEntries` (see the
  `tool-authorization` rule). `connected-tools/SKILL.md` gets a `## telegram` section while the
  tool is available; integration and channel CRUD re-render it

## SSH host document (/etc/agents)

`tools/ssh/host_document.go`. On the first command of a scope (`X-Incident-ID`) on a host,
`executeOnServer` opens a second session on the same connection, pipes the loader script to
`sh -s`, and returns the result as `host_document` on that `ServerResult` (spec: etc-agents.org).

- the script applies the spec's trust check (uid 0, no group/other write) on the host; skipped
  files come back in `skipped_untrusted_files`, never in `content`
- a first command that is a write command is held once (`hostDocumentWriteHold`) when the host
  has trusted content, so the agent reads the document before its first change; the retry runs
- a read failure is logged and never blocks the command; the next command tries again
- "already read" is in memory per scope + user@address:port for 12 h; a gateway restart resets it
- content limit 32 KiB (kept from the start, cut on a rune boundary, `truncated: true`)
- setting `read_host_document` (default true) turns it off per SSH instance
- the rules for the agent travel in `host_document.note`; do not move them into skill prompts

## SSH command policy

Read-only mode (`command_validator.go`) blocks write commands unless the host has
`allow_write_commands` (ad-hoc hosts: `adhoc_allow_write_commands`). On top of it, each SSH tool
instance has an operator policy (`command_policy.go`, `ValidateCommandWithPolicy`) for all its hosts:

- `ssh_allowed_commands` / `ssh_denied_commands`: textarea settings, one pattern per line (`#`
  comments); a JSON array of strings is accepted too. Words match words; `*` in a word matches
  any characters; a lone `*` is one word, or any remaining words when it is last
- order: deny (always, also with write access; matches the start of each simple command after env
  assignments, `sudo` and the program path; `$(...)` and backtick contents are checked too) →
  write access → allow (whole simple command, never one with `$`, a backtick, `<`, `>`, `&`, `(`,
  `)`, `{`, `}` or a backslash) → the
  read-only rules for every part an allow pattern does not cover
- without allow patterns the read-only path is `ValidateCommand` unchanged. A deny is a guard
  rail, not a sandbox: with write access `sh -c` can hide a command
- `connected-tools/SKILL.md` lists both lists for the agent (`extractToolDetails`), and a blocked
  error appends the allow patterns
- an allowed write command is still a write for the host-document hold (`ValidateCommand(cmd,
  false)`): it waits once for the agent to read the host document

## Gitea tool: gitea.*

`tools/gitea`, `registerGiteaTools` (`registry_gitea.go`). Read pull requests for pre-merge review;
Forgejo speaks the same API.

- settings: `gitea_url` (a trailing `/api/v1` is dropped), `gitea_token` (secret, sent as
  `Authorization: token …`), `gitea_allowed_repos` (textarea: `owner/repo` or `owner/*` per line;
  empty = all the token can read), `gitea_allow_comments` (default off), `gitea_verify_ssl`,
  `gitea_timeout`
- a pull request is `repo` + `number` or `pr_url`; a `pr_url` on another host is refused. The
  allowlist is checked before any request
- outputs are compact JSON, not raw API objects. Diffs are split per file from `pulls/{n}.diff`
  (renames match the old path too) and cut at `max_bytes` (default 50000, max 200000) with a
  `[TRUNCATED …]` marker; files come from `/raw/` and binary files are not shown
- listings page by `X-Total-Count` (Gitea may cap the page size), at most 20 pages
- `create_pull_request_comment` re-reads `gitea_allow_comments` from the DB on every call (no cache)
- no proxy toggle and no env proxy; redirects are refused (the token would follow them)

## Google Drive tool: google_drive.*

`tools/googledrive`, `registerGoogleDriveTools` (`registry_googledrive.go`). Drive search and read
(Docs/Slides → text export, Sheets → CSV per tab through the Sheets API, text files via `alt=media`;
other types are refused with the link), Sheets `sheets_get_values` (no range = tab list),
`sheets_append_rows`, `sheets_update_values`.

- auth: a service-account JSON key (`gdrive_service_account_json`, secret textarea). The gateway
  signs an RS256 JWT (RFC 7523, no Google SDK) and always posts it to
  `https://oauth2.googleapis.com/token`; the key's `token_uri` is ignored, so a key cannot send the
  assertion elsewhere. Optional `gdrive_impersonate_user` = `sub` claim (domain-wide delegation).
  Access tokens are cached per key id + subject + scope until 2 min before expiry
- reads ask for `drive.readonly spreadsheets.readonly`; only writes ask for `spreadsheets`. Writes
  re-read `gdrive_allow_writes` from the DB on every call (default off) and are refused before any
  Google call. Caps: 1000 rows / 50000 cells per write; cells are string, number, bool or null
- access control is Google's own: the service account sees only files shared with its
  `client_email` (Editor for writes). `extractToolDetails` shows that email to the agent
- files/spreadsheets are ids or docs/drive.google.com links (`ParseFileID`; other hosts refused).
  A range without `!` that is not an A1 cell range is a tab name and gets quoted (`NormalizeRange`)
- Google is a fixed public endpoint, so this tool uses the runtime `HTTP(S)_PROXY`
  (`ProxyFromEnvironment`), unlike the other built-in tools; closed networks reach Google through it.
  Redirects only to https, at most 5; Go drops `Authorization` on a cross-host redirect
- outputs are compact JSON; text is cut at `max_bytes` (default 50000, max 200000) with
  `[TRUNCATED …]`; `sheets_get_values` returns `max_rows` rows (default 200, max 5000)

## Git repository mirrors: git_repo

A tool type with **no gateway functions**. The API (`internal/services/git_repo_sync.go`, started
from `main.go`) keeps one shallow mirror per enabled instance at `/akmatori/repos/<logical_name>`
plus `<logical_name>.status.json`; the worker mounts `repos` read-only (compose and Helm) with
`safe.directory=*` and `GIT_OPTIONAL_LOCKS=0`. The settings schema lives in the gateway
(`getGitRepoSchema`) only for the UI form; `connected-tools` gets a hand-written bash example and
the path, branch, window and last error from the status file.

- settings: `git_url` (https, http, ssh, scp-like; no credentials and, for http(s), no user name in it), `git_branch` (empty =
  remote default), `git_auth` none|token|ssh_key, `git_token` / `git_ssh_private_key` (secrets),
  `git_username`, `git_ssh_known_hosts` (empty = accept-new, pinned in the state dir),
  `git_history_days` (30; 0 = newest commit), `git_sync_interval` (300 s, min 60),
  `git_max_size_mb` (2048)
- credentials go only into git's environment (`http.extraHeader` via `GIT_CONFIG_*`, a temporary
  0600 key file in `/akmatori/git-state`), never into the mirror's `.git/config`, which agents can
  read. `git-state` is not mounted into the worker. `GIT_ALLOW_PROTOCOL=https:http:ssh`; URLs
  starting with `-`, `ext::` and `file://` are refused; the remote host must not resolve to
  loopback, link-local or multicast (private ranges are allowed). For http(s) the checked
  addresses are pinned with `http.curloptResolve` and `http.followRedirects=false`, so a later DNS
  answer or a redirect cannot move the fetch; the env proxy (`HTTP(S)_PROXY`, `ALL_PROXY`) is not
  used, so the git host needs direct egress; ssh is checked before the dial only
- a 30 s loop syncs due mirrors one at a time: clone `--depth 1` into a `.tmp-*` dir, then
  `fetch --shallow-since` for the window, then rename; later fetch + `reset --hard`; once a day
  re-apply the window (`git_history_days=0`: every fetch is `--depth=1`), `reflog expire` and
  `gc --prune=now`; a window with no commits in it (idle repo) falls back to the newest commit,
  and a failed trim is recorded in the status and retried. A url/branch/auth change re-clones; a failure
  before the old mirror is removed (DNS, refused address) keeps the old fingerprint in the status, so
  the change is still applied on a later pass;
  over `git_max_size_mb` the mirror is removed and not retried until a setting changes
- disabled or deleted instances lose their mirror. A directory Akmatori did not create gets a
  status with `unmanaged: true` and is never written to or deleted on any later pass (settings
  change, disable, delete); once the operator removes it, the next pass clones
