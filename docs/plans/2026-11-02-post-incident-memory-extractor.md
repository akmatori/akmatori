# Post-incident memory extractor and evidence-graded memories

Status: revision 3. PR 1 (evidence contract) implemented 2026-11-03 on branch
`feat/memory-evidence-grades` (stacked on PR #61), review fixes in `2b3d31b`. PR 2 and PR 3 open;
PR 4 (usage and retirement) proposed. Revision 3 (2026-11-03) adds secret redaction, retry/lease
job mechanics and the PR 4 design, marked **(rev 3)**. Prepared 2026-11-02 against branch `feat/gateway-knowledge-tools` at bf60ffc
(B1 of the 2026-11-01 plan: gateway knowledge tools, no subagents). B2 of that plan (remove the disk
mirrors) is independent of this one.
Revision 2, same day, after the Codex review in
`2026-11-02-post-incident-memory-extractor-codex-review.md`. Every review item tagged [high] is
addressed; the changes are marked **(rev 2)**.

## Problem

The investigating agent writes memory with `memory.write` at the end of its run, before the outcome of
the incident is known. A reported case: an incident resolved by itself, the agent saved "high load
caused the data gap" as a fact, and the alert timeline shows the load rose *after* the gap started. The
load was an effect. The memory now reads as a known fact in every later investigation of that service.

Three things cause this:

1. **Write side.** The agent that formed the hypothesis also decides that it is a fact. Nothing in the
   loop knows the outcome (did an action fix it, did it resolve alone, did it recur).
2. **Storage.** `memories` has no field that separates an observed fact from an unverified cause. The
   manifest (`RenderMemoryManifest`) shows `name | type | description`, so a reader cannot tell.
3. **Read side.** `memoryRecallInstruction` presents memory as knowledge to apply, not as leads to
   check.

### What OpenAI Codex does (reference)

Read from `github.com/openai/codex` at commit `adca203` (2026-11-02): `codex-rs/memories/README.md`,
`codex-rs/memories/write/templates/memories/stage_one_system{,_v2}.md`,
`consolidation{,_v2}.md`, `codex-rs/ext/memories/templates/memories/read_path_v2.md`,
`codex-rs/protocol/src/memory_version.rs`.

- The working agent does not write memory. A separate extraction pass runs later over the transcript
  and writes a per-session record; a consolidation agent merges records into a summary that every
  new session receives. The working agent only appends a note when the user explicitly says
  "remember".
- The extraction prompt separates "observed evidence, user-authorized actions, implemented changes,
  proposals, hypotheses, and uncertainty" and labels each task `success | partial | fail | uncertain`;
  "only the assistant claims success without validation" is `uncertain`.
- Consolidation downgrades or drops one-off judgments unless later evidence shows they held;
  "fresher validated evidence usually wins".
- The read prompt says "memory is not proof of current behavior"; answers cite the memory they used.
- Memories carry `usage_count` / `last_usage`; unused ones drop out of consolidation.

Codex has no temporal-order rule for causes; that is our own addition.

## Goal

Memory that comes from incidents is written by a post-incident pass that knows the outcome, not by
the investigating agent during the run. Every memory carries an evidence grade. **(rev 2)** No
automatic path grades a causal claim above `unverified`; only an operator can mark a memory
`confirmed`. Recall treats an unverified memory as a lead to check, not as a fact.

## Design

### D1. Evidence grade on memories

Add to `memories` (API `internal/database/models_context.go`, gateway mirror
`mcp-gateway/internal/database/models_proposals.go`):

| column | type | meaning |
|---|---|---|
| `evidence` | varchar(16) not null | `confirmed` \| `observed` \| `unverified` |
| `claim` | varchar(16) not null default '' | `causal` \| `factual`; '' = not assessed (legacy rows) |
| `evidence_note` | varchar(500) | why: timestamps, what resolved it, source incident(s). Shown in UI and `memory.get` |

**(rev 2)** Dropped from revision 1: `recall_count`, `last_recalled_at`, usage-based decay, automatic
contradiction deletion, automatic `verified`. See D6 and "Not in scope".

Grades:

- `confirmed` — an operator set it, on purpose, in the UI or API. Nothing else produces it.
  **(rev 2)** Authorship is not verification: an operator-written row defaults to `observed`; a
  Slack feedback row and a from-text draft are `observed`; proposal approval keeps the grade the
  proposal carries (default `observed`).
- `observed` — a fact seen directly and not a cause: host config, tool quirk, a timeline, "this alert
  resolved by itself within 10 minutes". The grade of a context-file summary.
- `unverified` — a cause nobody confirmed, or a legacy row nobody assessed.

Rules that code enforces, not the model:

- `claim = causal` ⇒ `evidence = unverified` for every non-operator write (extractor D3, agent
  `memory.write` D4, proposal approval without an explicit grade). The writer cannot override it.
  **(rev 2)** The model still chooses `claim`; it can mislabel a cause as `factual`. The prompts
  define `causal` as "any statement of the form X caused / led to / explains / because Y", and the
  extractor's post-processing also marks `claim = causal` when the description or body matches a
  small regex list (`caused by`, `root cause`, `because`, `led to`, `due to`, `triggered`). That is a
  net, not a guarantee; the UI shows `claim` so an operator can fix it.
- `cause_at` / `effect_at` (D3) only add text to `evidence_note`; they never raise the grade.
- Context-file summaries: `observed`, `claim = factual`, set by `SaveFileSummary`.
- `UpdateMemory` (UI/API): an operator may set any grade, change `claim`, and clear the note (`null`
  clears, omitted keeps). A content change does not reset the grade: the operator who edits sees it.
- Upsert "only update when different": the predicate compares `evidence`, `claim` and `evidence_note`
  with `COALESCE(…, '')` on the nullable ones **(rev 2)**, so a NULL → value change is a change.

Validation: the agent rules have their own shared fixture `tests/fixtures/memory_evidence_cases.json`
(run by both modules). Operator writes refuse a note over 500 bytes; agent writes cut it UTF-8
safe. **(PR 1)** The causal net also matches "caused the …" / "causing" (`caus(e|es|ed|ing)`); the
first draft only matched "caused by", and a proposal test caught it.

Migration **(rev 2, deterministic)**: add the columns; backfill `evidence`:
`created_by = 'operator'` → `observed`; `source_file_id IS NOT NULL` → `observed`; every other row →
`unverified` with `evidence_note = "legacy: not assessed"`. `claim` stays NULL for legacy rows. Then
set `NOT NULL DEFAULT 'unverified'` on `evidence`. **(PR 1)** As built: AutoMigrate adds the
column with that default (so every old row starts `unverified`), then one UPDATE sets operator rows
and file summaries to `observed` and one sets the legacy note; it runs only in the start that adds
the column. Checked on SQLite (unit test) and on PostgreSQL 16 (embedded, by hand). SQLite (tests) and PostgreSQL both take the
`ALTER TABLE … ADD COLUMN` + `UPDATE` form the existing migrations use.

Other write surfaces that carry the fields **(rev 2)**: REST `MemoryRequest` DTO and
`GET /api/memories?evidence=`, the export zip front matter (`evidence:`, `claim:`,
`evidence_note:`; the disk ingest reads them while B2 is open), `SaveFileSummary`, gateway
`registry_knowledge.go` tool schemas, OpenAPI.

Proposals **(rev 2)**: `proposalMemoryContent` gets `evidence`, `claim`, `evidence_note`. The
snapshot at `proposals.create` stores them; the staleness compare includes them; a pending proposal
created before this change has no fields in its snapshot → those fields are not compared (compat).
`Approve` applies the content's grade through `UpsertByName`; missing → `observed`; `claim = causal`
→ `unverified` (the rule above). The proposal-editor prompt names the fields.

### D2. Incident runs no longer write memory

- Gateway: inside `requireRun` (`mcp-gateway/internal/tools/knowledge/tool.go`), after the existing
  checks (run exists, scope matches, status running, not a proposal run), add: `run.IncidentUUID != ""`
  → refuse `memory.write` and `memory.delete` with "alert investigations do not write memory: the
  post-incident pass records findings when the incident closes; put the timeline and findings in the
  final answer". **(rev 2)** The order keeps the proposal refusal and the quota/gate checks intact.
- Prompts **(rev 2, run-specific)**. `incident-manager` is the root skill of both incident runs and
  request runs (`SpawnIncidentManager` vs `PrepareRequestScope("incident-manager", …)`), and every
  SKILL.md is shared by all runs, so the text cannot be a blanket replacement:
  - SKILL.md "Record durable findings" becomes neutral: "Memory writes are allowed in request work
    (Slack mention, API task, cron) and refused in alert investigations, where a post-incident pass
    records findings. When allowed: <the current rules> plus the `claim` / `evidence` rules of D4."
  - AGENTS.md is rendered per run. `prepareScopeDir` gets a `runKind` argument: incident runs get
    "Do not call memory.write. Write the timeline with timestamps (what moved first) and your
    findings, marked as observed or hypothesis, in the final answer; the post-incident pass reads
    it." Request runs get the current write guidance.
- Operator "remember …" in an incident Slack thread **(rev 2)**: the feedback classifier is
  best-effort (0.6 threshold, rejects commands, needs an LLM), so it is not a guarantee. The
  incident-run AGENTS.md says: "When an operator asks you to remember something, repeat it in the
  final answer under `Operator asked to remember:`." The extractor (D3) turns that block into an
  `observed` memory with `evidence_note = "operator asked in incident <uuid>"`. A dependable
  `/remember` command is a follow-up.

### D3. Post-incident extractor

`IncidentMemoryExtractor` in `internal/services/incident_memory_extractor.go`: a one-shot LLM
through `OneShotLLMCaller`, one job at a time, **(rev 2)** driven by a durable queue on the incident
row instead of fire-and-forget hooks.

**Queue (rev 2).** New columns on `incidents`: `memory_extraction` varchar(16)
(`'' | pending | running | succeeded | no_output | skipped | failed`), `memory_extraction_at`,
`memory_extraction_error` (≤ 300 bytes), **(rev 3)** `memory_extraction_attempts` (int) and
`memory_extraction_retry_at` (nullable time). Every successful close transition sets
`memory_extraction = 'pending'` **inside its own transaction**, in the same UPDATE that sets
`status = closed`: `CloseIncident` (manual), `MonitorSweepService.RunSweep` (the one bulk UPDATE),
`StaleCloseService.closeBatch` (the UPDATE that re-applies the staleness predicate — so only rows that
really closed are queued). `IncidentMerger` sets the merged child to `skipped` (reason "merged into
<uuid>"); the survivor is extracted when it closes, with the merge family (below).

A worker loop (`Run(ctx)`, started from `main.go` before the startup sweeps, ticker 30 s plus a
`Kick()` channel the close paths and the rerun endpoint call) claims one row at a time:
`UPDATE incidents SET memory_extraction='running', memory_extraction_at=now WHERE uuid=? AND
memory_extraction='pending' AND status='closed' AND (memory_extraction_retry_at IS NULL OR
memory_extraction_retry_at <= now)` (RowsAffected = 1 is the claim). Startup turns
`running` back into `pending` (a crash mid-job re-runs it; the apply step is idempotent, below). A
job on a missing incident (retention) ends as `skipped`.

**Retry and lease (rev 3, as Codex's `jobs` table: `retry_at`, `lease_until`, attempts).** A
transient failure (no worker, LLM timeout, a provider error, invalid JSON) does not end the job: the
worker sets `pending`, `memory_extraction_attempts += 1`, `memory_extraction_retry_at = now +
backoff` (2 min, 10 min, 1 h; after 3 attempts the row is `failed` with the last error). The
backoff keeps one broken incident from blocking the loop: the claim skips rows whose retry time is
in the future and takes the next one. `memory_extraction_at` is also the lease: a row `running` for
longer than 15 min is claimable again by the same predicate (`OR (memory_extraction='running' AND
memory_extraction_at < now - 15 min)`), so a job stuck in a live process (not only a crashed one)
is re-run without a restart; the apply step's re-check of `memory_extraction_at` makes the stale
job's late apply a no-op. The rerun endpoint resets attempts and retry time. Outcomes are distinct
and visible in the UI (D7): `succeeded` (memories written), `no_output` (a valid run with nothing
durable), `skipped` (gate, merged, missing), `failed` (after the retries).

**Gate.** `MemoryGenerationEnabled` read at claim time → `skipped`, and again in the apply step (the
rule: every mutation checks the gate). Alert-sourced incidents only; a request scope never has the
column set.

**Eligibility and outcome facts (rev 2).** The extractor computes no outcome label that grades
anything. It passes facts to the prompt and lets the model describe the outcome in the timeline:

| fact | source |
|---|---|
| close reason | `closed_reason` (`manual` / `monitor_expired` / `auto_stale`) |
| how each alert resolved | new `alerts.resolved_by` varchar(16): `source` (the monitoring system sent a resolve, `ResolveAlertTx` from `processResolvedAlert`), `operator` (manual close), `sweep` (monitor sweep, stale close). Stamped by each path in the same UPDATE as `resolved_at`. Legacy rows: NULL = unknown |
| agent self-report per run | `agent_runs.outcome` of every finished run (`resolved` / `unresolved` / `escalate` / empty), **not** `incidents.result_status` (it keeps an older run's verdict) |
| timestamps | `started_at`, each run `started_at` / `completed_at`, each alert `fired_at` / `last_seen_at` / `resolved_at`, incident `resolved_at` (there is no `closed_at`) |

Eligibility table:

| case | action |
|---|---|
| no finished run with a non-empty answer | `no_output` ("nothing to extract") |
| zero alert rows (unlinked or deleted) | eligible; the prompt says "no alert rows: do not claim anything resolved" |
| newest run still `running` (manual close of a running incident) | not claimable yet: the claim predicate adds `NOT EXISTS (run running)`; the row stays `pending` and the next tick retries |
| `status = failed` | never closes, never queued (same as today: failed is terminal) |
| `status = merged` | `skipped` |
| survivor of a merge | runs of `incident_uuid IN (survivor, children where merged_into_uuid = survivor)`, alerts of the survivor (the merge re-points them); the prompt lists the family UUIDs |
| reopened after close (Slack follow-up sets `running`, `completeIncident` later writes `completed`/`monitor`, a sweep closes again → `pending` again) | the second extraction is idempotent: memories with `incident_uuid = this` and the same name are updated in place; the rest are created |

**Stable inputs (rev 2).** The worker snapshots the inputs after the claim, calls the LLM, and in the
apply transaction re-checks `status = 'closed' AND memory_extraction = 'running' AND
memory_extraction_at = <claim time>`. A mismatch (reopened, re-closed, or re-claimed after a restart)
aborts the apply with no write and leaves the row for the next claim.

**Input** (capped, marked as data):

- the facts table above, rendered as text, plus the first alert's raw text ("Original alert text")
- transcript **(rev 2)**: `full_log` of every finished run of the family from the DB, oldest first
  (the log holds the agent text and the tool traces; `session_export.jsonl` in the scope dir is
  overwritten by each run and is used only as the newest run's richer copy when it exists). Per-run
  cap 32 KiB with head 8 KiB + tail 24 KiB (the final answer is at the tail), total 96 KiB. Tool
  results the worker spilled to `tool_outputs/` in the scope dir are not read in v1 (bounded file
  reads of the work dir are a follow-up; the agent's final answer is asked to carry the timestamps
  the pass needs, D2)
- the final answers (`response`) of the family's runs in full, newest first, 16 KiB total
- operator feedback memories with `incident_uuid` in the family, 8 KiB
- the memory manifests of the skill scope (`last_skill_used` of the newest run) and `global`
  (names + descriptions, already capped), so the extractor can link with `[[name]]`. **(rev 2)** It
  never updates another incident's memory: it has no body to preserve, and Dreaming owns
  consolidation (D6)

**Output** — strict JSON:

```json
{
  "timeline": [{"at": "2026-11-02T10:02:00Z", "what": "ingest gap starts (metric X)", "source": "tool: prometheus query"}],
  "memories": [
    {
      "scope": "global | <skill>",
      "name": "<slug>",
      "type": "fact",
      "claim": "causal | factual",
      "description": "<one line>",
      "body": "<markdown, absolute dates, [[links]] to manifest names>",
      "evidence_note": "<what in the transcript or alerts supports this>",
      "cause_at": "2026-11-02T10:05:00Z",
      "effect_at": "2026-11-02T10:02:00Z"
    }
  ]
}
```

At most 5 memories per incident. The prompt (new constant next to `memoryDrafterSystemPrompt`):

- "You are a post-incident pass. The investigation is over. Facts: <facts table>. Extract what a
  future investigation of a similar alert must know."
- "Separate what was observed (tool output, alert timestamps) from what the agent concluded. The
  agent's conclusions, including its `resolved` self-report, are claims, not evidence. An alert that
  an operator or a sweep resolved was not observed to recover."
- "A causal claim ('X caused Y', 'because', 'root cause') is `claim: causal`. Give `cause_at` and
  `effect_at` from the data when you can; if the cause is not before the effect, or you cannot find
  both, write that in `evidence_note`. The system stores every causal claim as unverified; your job
  is to make the note useful."
- "For an incident with no action by the agent or an operator, prefer one `factual` memory with the
  timeline over any causal memory."
- "An `Operator asked to remember:` block in a final answer becomes a `factual` memory, verbatim."
- "Never save what the alert, a runbook or a context file already says. Return an empty `memories`
  list when nothing is durable."

**Post-processing in Go** — the model never sets `evidence`:

1. parse; invalid JSON → `failed`; empty list → `no_output`
2. each memory: `normalize` + `validate` (shared rules), the causal regex net (D1), grade:
   `causal → unverified`, `factual → observed`; `cause_at`/`effect_at` present and out of order →
   append "order check: cause <t> is after effect <t>" to the note; `created_by = agent`,
   `incident_uuid` = this incident (the survivor for a family)
3. **(rev 2) apply in one transaction** through a new `MemoryService.ApplyExtraction(tx, incidentUUID,
   claimAt, []Memory)`: re-check the claim (above); for each memory, if a row with the same scope +
   name exists and `incident_uuid = this` and it is agent-owned → conditional UPDATE with the
   protection predicate in the SQL (`created_by <> 'operator' AND source_file_id IS NULL`, the same
   predicate the gateway's upsert uses — and a shared fixture test proves both modules refuse the
   same rows); if the name exists and belongs to anyone else → create under a unique name
   (`CreateWithUniqueName`, `-2`, `-3`); else create. Status → `succeeded` in the same transaction.
   A partial failure rolls everything back, so a rerun never leaves suffixed duplicates
4. the timeline is stored on `incidents.memory_timeline` (text, for the UI; not a memory)
5. after commit: `RefreshAfterMemoryChange` (SKILL.md manifests + disk mirror). A mirror failure is
   logged; it does not fail the job (today `CreateMemory` reports failure after a successful DB
   write when the sync fails — the extractor does not inherit that)

**Secret redaction (rev 3, decided 2026-11-03).** `internal/services/secret_redaction.go`,
`RedactSecrets(string) (string, int)`: best-effort regex replacement with `[REDACTED_SECRET]`, as
Codex's `codex_secrets::redact_secrets` (it runs on the serialized rollout before the prompt upload
and on every output field). Patterns, the same set plus two: `Bearer <token>`, `sk-…` keys, AWS
`AKIA…` ids, `api_key | token | secret | password [:=] value` (value ≥ 8 chars), URL userinfo
(`scheme://user:password@host` → the password), and `-----BEGIN … PRIVATE KEY-----` blocks. A
shared fixture `tests/fixtures/secret_redaction_cases.json` pins the patterns; the gateway mirrors
the function (`knowledge/redact.go`) and runs the same fixture. Applied:

- to the extractor input: the transcript, the final answers and the alert text, before the prompt
  is built (the model must not receive what it must not store; the agent already saw the secret
  during the run, but the one-shot call may go to another provider)
- to every extractor output field (description, body, evidence_note, timeline `what`) before
  `ApplyExtraction`; the number of replacements is logged and, when non-zero, kept on the job as a
  note (`redacted N secrets`) without failing the job
- to `memory.write` in the gateway (D4) and to proposal memory content on approval: the same
  function, so a request run or Dreaming cannot store a token either

Not covered: secrets with no recognisable shape (a bare password in prose). The transcript stays in
`agent_runs.full_log` as today; this plan does not change the run log.

**Fallback with no LLM** (`ErrWorkerNotConnected`, timeout 50 s): retried with the backoff above,
then `failed`, no memory, the incident
stays closed. `POST /api/incidents/{uuid}/extract-memory` **(rev 2)**: incident must be `closed`;
status `succeeded | no_output | skipped | failed | ''` → set `pending`, `Kick()`, 202;
`pending | running` → 409; `merged` → 409; missing → 404. A rerun replaces the prior output of this
incident (same names update in place). Backfill of incidents closed before the upgrade: not
automatic; a loop over the rerun endpoint for the last N days.

Retention **(rev 2)**: today `cleanupExpiredIncidents` deletes `completed | failed | diagnosed`
rows and never `closed` ones (an existing inconsistency, out of scope here). A deleted incident has
no row to claim; the worker skips it.

Cost: one one-shot call per closed alert incident, 30–130 KiB input. The input caps are the
budget; the number of incidents per day is an install property, not a plan claim.

### D4. `memory.write` for request work

Stays as in B1. The tool schema (`registry_knowledge.go`) gets `claim` (default `factual`),
`evidence` (`observed | unverified`, default `observed`) and `evidence_note`. The gateway applies the
D1 rule (`causal → unverified`) and the causal regex net, and rewrites any other `evidence` value to
`unverified` with a note. Caps, provenance, protection, gate: unchanged. Dreaming is the main caller.
**(rev 3)** Description, body and note go through `RedactSecrets` before the upsert; the response
carries `redacted: N` when it replaced something.

### D5. Recall side

- `renderManifest` gets an `evidence` column: `| name | type | evidence | description |`. The
  column costs ~12 bytes a row inside the existing byte and line caps; the cap tests stay green.
- `memoryRecallInstruction` adds: "`evidence` says how much to trust a memory. `confirmed`: an
  operator confirmed it. `observed`: a fact seen directly. `unverified`: an unconfirmed cause or an
  unassessed legacy note — a lead: check it against live data before you act on it, and say
  'unverified, from memory <name>' when you use it."
- `memory.search` / `memory.list` / `memory.get` return `evidence`, `claim`, `evidence_note`,
  `incident_uuid`; `memory.list` takes an `evidence` filter. **(rev 2)** No recall counter.
- The incident-manager prompt step that reads memory adds: "When a memory contradicts what the tools
  show now, trust the tools and say so in the final answer under `Memory contradicted:`." The
  extractor records that block as an `observed` memory that links the contradicted name
  (`[[name]]`), so Dreaming sees both; nothing is deleted automatically.

### D6. Dreaming

**(rev 2)** Reduced to what the cron can guarantee. The prompt (`dreamingCronPrompt`; append the old
text to `legacySystemCronPrompts`) gets:

- when merging duplicates, the kept memory takes the *lowest* grade of the cluster unless an operator
  `confirmed` one (keep that one as is), and the note lists the source incidents
- never write `evidence: confirmed`
- a memory with `Memory contradicted:` evidence (D5) linked to it: rewrite or delete the contradicted
  agent memory only when the newer observation is specific; otherwise leave both

Legacy grading is the migration (D1), not Dreaming. Corroboration counts and decay need durable
per-incident provenance (one `incident_uuid` per row is not enough); **(rev 3)** that is D8 / PR 4.

Why Akmatori's consolidation is not Codex's. Codex consolidates files: a git baseline of the memory
directory, a workspace diff, and an agent that rewrites one summary (≤ 10 KB) injected into every
session. Akmatori is database-authoritative: the manifest in SKILL.md is rendered from the DB with
no model (D5), and Dreaming is the model pass over `memory.list` with a `since` cursor, which is the
diff. The two Codex behaviours that we do not have are usage-ranked selection and support
propagation (a claim loses support when its source goes away); both are D8.

### D8. Usage tracking and retirement **(rev 3, proposed PR 4)**

Codex keeps `usage_count` / `last_usage` per memory (`stage1_outputs`), fed by a citation block
the model appends to its final reply, and consolidation selects by usage first and drops memories
unused for `max_unused_days`. Akmatori can get most of the signal server-side, with no model
cooperation, because every read goes through the gateway with `X-Run-ID`:

- new table `memory_uses` (`memory_id`, `run_id`, `incident_uuid`, `kind` = `search` (returned as a
  hit) | `get` (read in full) | `cited` (named in the final answer), `at`), plus denormalised
  `memories.use_count` and `memories.last_used_at` (updated in the same statement as the insert;
  `search` hits count only for `last_seen_at`, not `use_count`, so a broad search does not inflate
  everything it returns)
- citation: the incident-manager prompt asks for a final-answer block `Memory used: [[scope/name]]
  …` listing the memories the answer relied on (the same shape as `Memory contradicted:`, D5). The
  output parser (`internal/output`) reads it in `FinishRun` and inserts `cited` rows; a name that
  does not exist is ignored. This is the only signal for a memory read from the manifest without a
  `memory.get`
- recall: the manifest orders by `last_used_at` descending, then `updated_at` (today: `updated_at`
  only), so what runs actually use stays in the 150-entry cap; `memory.list` returns `use_count`,
  `last_used_at` and takes `unused_since`
- retirement (Dreaming, not code): an agent `unverified` memory with `use_count = 0` and
  `created_at` older than 90 days, or `last_used_at` older than 90 days, is a candidate; Dreaming
  deletes it or merges it, and never touches `confirmed` or operator rows. A code job deletes
  nothing
- support propagation: when an incident is deleted by retention, or an operator reruns extraction,
  the memories with that `incident_uuid` that no later source confirmed get the note `source
  incident deleted` (or are replaced by the rerun); Dreaming reviews them. `memory_uses` rows
  follow the memory (`ON DELETE CASCADE`)
- UI: use count and last use on the memory list and detail; the incident page lists the memories
  the runs used

Not in PR 2 because it is independent of extraction and PR 2 is already the largest change; it
needs `memory_uses` before any decay rule can be honest.

### D7. UI

- Memory list (`web/src/components/memory/MemoryList.tsx`): evidence badge (green confirmed, grey
  observed, amber unverified), filter by evidence, `claim` shown on hover.
- Memory detail: `evidence_note`, `claim`, link to the source incident; the operator can set
  `confirmed` (with a tooltip: "you checked this; the agent will treat it as fact").
- Incident page: a "Memory" section — extraction status + error, rerun button, the timeline, the
  memories from this incident (`GET /api/memories?incident_uuid=` exists).
- Memory form: evidence selector, default `observed`.

### Not in scope, follow-ups

- Automatic `verified` from outcomes. The review showed every automatic signal we have is
  administrative or a self-report (`CloseIncident` and both sweeps stamp `resolved_at = now`; a
  fix can land while the agent still runs). Revisit with an action log (which tool call changed
  what, when) and the operator thumbs-up.
- Corroboration across incidents (the same claim from several incidents raises nothing by itself;
  D8 gives the data, the rule is a later decision).
- A dependable `/remember` for incident threads.
- Bounded reads of `tool_outputs/` spill files by the extractor.
- SRE Bench scenario "effect before cause" (`akmatori-bench`): the first metric the agent sees is the
  effect, the real cause is earlier and quieter.
- Retention of `closed` incidents (existing gap).

## Implementation **(rev 2: three PRs)**

| # | PR | contents | tests |
|---|---|---|---|
| 1 | Evidence contract | D1 columns + migration + backfill (API and gateway mirror), shared fixture cases, `validate`, NULL-aware upsert predicate, REST DTO + filter, export/ingest front matter, `SaveFileSummary`, proposals (content, snapshot, staleness compat, approve), gateway schemas + `memory.*` responses + `list` filter, D5 manifest column and recall text, D4 `memory.write` fields and rules, OpenAPI | fixture both modules; migration on SQLite (+ PG matrix if present); backfill table; upsert "unchanged" with equal fields and "changed" on NULL→value; proposal: old snapshot without fields stays approvable, new snapshot detects an evidence change; gateway: `causal` forced `unverified`, `confirmed` from agent rewritten; manifest caps |
| 2 | Extraction | D2 gateway refusal + run-specific prompts; D3: `alerts.resolved_by` stamped by all four resolve paths, queue columns **(rev 3: + attempts, retry_at)**, close paths set `pending` in-transaction, merge sets `skipped`, worker loop + claim + startup reset **(rev 3: + backoff, lease re-claim)**, input reducer, **(rev 3) `RedactSecrets` on input and output, shared fixture, gateway mirror on `memory.write` and proposal approval**, prompt, grading, `ApplyExtraction` transaction, rerun endpoint, `Kick` wiring in `main.go`; docs (`memory-and-runbooks.md`, `alerts-correlation-and-monitor.md` close paths, `agent-runs.md`) | gateway: alert run refused for write and delete, slack/cron run allowed, proposal run still refused first; prompts: incident AGENTS.md has no write guidance, request AGENTS.md has it; `resolved_by` per path; close paths queue only rows that closed (stale close with an alert landing in between stays open and unqueued); claim is exclusive under two workers; reopened incident aborts apply with no write; crash mid-job → startup → re-run → no duplicates; partial apply rolls back; rerun endpoint matrix (closed/pending/running/merged/missing); merge family gathers children's runs; reducer caps; fake caller end-to-end with the reported case: self-resolved, "load caused gap" → `unverified` with the order-check note, no `observed` causal row; no LLM → retried with backoff, then `failed`, incident stays closed; a stuck `running` row is re-claimed after the lease; a late apply of the stale job is a no-op; gate off → `skipped`; redaction fixture in both modules; a transcript token never reaches the prompt or a memory |
| 3 | UI + Dreaming | D7, D6 prompt + legacy append | `make test-web`; prompt seed/legacy tests |
| 4 | Usage and retirement **(rev 3, proposed)** | D8: `memory_uses`, `use_count` / `last_used_at`, gateway inserts on `get` / `search`, `Memory used:` block parsed in `FinishRun`, manifest order, `memory.list` fields + `unused_since`, Dreaming retirement rule + legacy append, retention note, UI | gateway: get/search insert rows, a search hit does not raise `use_count`; parser: cited rows, unknown names ignored; manifest order by last use; Dreaming prompt seed; retention sets the note |

PR 1 ships alone and is useful: grades become visible, agent causal writes are `unverified` from day
one. PR 2 is the behavior change: incident runs stop writing, the extractor writes back with grades.

## Rollout and risk

- Between PR 2's deploy and the first closes (monitor window, default 60 min; up to 3 days for stale
  close; longer for an incident held at `completed` by a firing alert), incident memory is quiet.
  Say so in the release notes.
- Memory volume: at most 5 per incident, against 20 writes per run today.
- Fail-open everywhere: no LLM → incidents close, memory stays, status shows `failed`.
- `unverified` is the honest default; it will label most legacy agent rows. The recall text makes
  the agent check them, not ignore them.
- Rollback: additive columns; the write path is one gateway condition and one prompt branch; the
  worker can be stopped by the gate.

## Decisions taken in revision 2 (were open questions)

1. Trigger at close, durable queue on the incident row, no second pass at completion.
2. No automatic `verified`. `confirmed` is operator-only.
3. Legacy rows: deterministic backfill to `unverified` (agent) / `observed` (operator, file).
4. `memory.write` stays available to request runs with the `causal → unverified` rule; incident
   threads get the `Operator asked to remember:` block until a `/remember` command exists.
5. The extractor keeps causal memories as `unverified`, including from self-resolved incidents
   (decided 2026-11-02 by the maintainer). It does not drop them. A hypothesis with its timeline
   and order-check note is a useful lead for the next investigation, and the recall text makes
   the agent check it before it acts.
6. **(rev 3, 2026-11-03)** Secrets are redacted on the extractor input and output, on
   `memory.write` and on proposal approval, with Codex's pattern set plus URL userinfo and private
   key blocks (decided by the maintainer).
7. **(rev 3)** Job mechanics follow Codex's `jobs` table: retry with backoff, a lease on
   `memory_extraction_at`, distinct `no_output`. Two columns more on `incidents`.
8. **(rev 3)** Usage tracking, usage-ranked recall and retirement are D8 / PR 4, proposed, not yet
   decided.
