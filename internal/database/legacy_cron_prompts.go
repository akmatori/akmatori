package database

// legacySystemCronPrompts holds every default prompt shipped for the system
// crons, verbatim (from git history). SeedSystemCronJobs and
// SeedImprovementEvaluatorCron replace a stored prompt that equals one of them
// with the current default; an operator-edited prompt is kept and named in a
// warning. Append the old text here whenever a default prompt changes.
var legacySystemCronPrompts = map[string][]string{
	dreamingCronName: {
		`You are running the nightly memory consolidation pass over the global cross-incident memory scope.

Goal: keep the global memory scope tight and high-signal. Focus only on the global scope and on memories updated in the last 14 days. Concretely:

1. List the candidates: gateway_call("memory.list", {"scopes": ["global"], "since": "<current time minus 14 days, ISO-8601>", "limit": 200}). While next_cursor is not empty, call again with "cursor": <next_cursor>. To check older memories for duplicates of a candidate, use gateway_call("memory.search", {"query": "<candidate description>", "scopes": ["global"]}). Read bodies with gateway_call("memory.get", {"id": <id>}).
2. For each duplicate or near-duplicate cluster, decide which memory to keep (prefer the most specific, most recent, operator-validated wording) and which to remove.
3. For each kept memory that should take facts from a duplicate, prepare a merged body.
4. Apply the changes one at a time:
   - gateway_call("memory.write", {"scope": "global", "name": "<kept name>", "type": "<type>", "claim": "<factual | causal>", "evidence": "<observed | unverified>", "evidence_note": "<what supports it; the source incidents>", "description": "<one line>", "body": "<merged body>"}) — the same name updates the memory.
   - gateway_call("memory.delete", {"scope": "global", "name": "<duplicate name>"}) — remove a duplicate or obsolete memory.
   Operator-authored memories and context-file summaries are protected (action "refused_protected"). Keep them as they are; when an agent memory duplicates one, delete the agent memory.
5. End with a concise summary of the pass: how many memories you scanned, merged and deleted. List the affected names.

Evidence rules. Every memory carries evidence: "confirmed" (an operator checked it), "observed" (seen directly, not a cause) or "unverified" (an unconfirmed cause, or an old note nobody assessed), and claim: "factual" or "causal".
- A merged memory takes the LOWEST evidence of its cluster: if any member is unverified, the kept memory is unverified. The one exception is a memory an operator confirmed: keep that one as it is, merge the others into it only when their facts do not change its claim, and never lower it.
- Never write "evidence": "confirmed"; only an operator can confirm. A causal claim ("X caused Y") stays unverified however many incidents repeat it.
- Put the source incidents of the merged facts in evidence_note.
- A memory whose body says "Memory contradicted:" (or that links a memory the tools proved wrong) is a newer observation about an older memory: rewrite or delete the contradicted agent memory only when the newer observation is specific (what was seen, where, when); otherwise leave both and note the contradiction in the older memory's evidence_note.

Do not touch any scope other than global.

If memory.list returns no memories or there are no clear duplicates, end with the one-line summary "no-op: nothing to consolidate".`,
		`You are running the nightly memory consolidation pass over the global cross-incident memory scope.

Goal: keep the global memory scope tight and high-signal. Focus only on the global scope and on memories updated in the last 14 days. Concretely:

1. List the candidates: gateway_call("memory.list", {"scopes": ["global"], "since": "<current time minus 14 days, ISO-8601>", "limit": 200}). While next_cursor is not empty, call again with "cursor": <next_cursor>. To check older memories for duplicates of a candidate, use gateway_call("memory.search", {"query": "<candidate description>", "scopes": ["global"]}). Read bodies with gateway_call("memory.get", {"id": <id>}).
2. For each duplicate or near-duplicate cluster, decide which memory to keep (prefer the most specific, most recent, operator-validated wording) and which to remove.
3. For each kept memory that should take facts from a duplicate, prepare a merged body.
4. Apply the changes one at a time:
   - gateway_call("memory.write", {"scope": "global", "name": "<kept name>", "type": "<type>", "description": "<one line>", "body": "<merged body>"}) — the same name updates the memory.
   - gateway_call("memory.delete", {"scope": "global", "name": "<duplicate name>"}) — remove a duplicate or obsolete memory.
   Operator-authored memories and context-file summaries are protected (action "refused_protected"). Keep them as they are; when an agent memory duplicates one, delete the agent memory.
5. End with a concise summary of the pass: how many memories you scanned, merged and deleted. List the affected names.

Do not touch any scope other than global.

If memory.list returns no memories or there are no clear duplicates, end with the one-line summary "no-op: nothing to consolidate".`,
		`You are running the nightly memory consolidation pass over the global cross-incident memory scope (/akmatori/memory/global/).

Goal: keep the global memory scope tight and high-signal. Focus only on the global scope. Process entries updated in the last 14 days — use the memory-searcher subagent to surface candidates from that window. Concretely:

1. Use the memory-searcher subagent to list current entries — search broadly enough to surface duplicates, near-duplicates, and stale rows among entries updated in the last 14 days.
2. For each duplicate or near-duplicate cluster, decide which entry to keep (prefer the most specific, most recent, most operator-validated wording) and which to remove.
3. For each kept entry that should incorporate facts from a soon-to-be-deleted duplicate, prepare a merged body.
4. Apply mutations one at a time via the memory-writer subagent. For each duplicate or superseded entry, emit Action: delete <slug> via memory-writer rather than rewriting both:
   - Action: upsert <slug> — write the merged or refreshed body (memory-writer is idempotent and overwrites by name).
   - Action: delete <slug> — remove a duplicate or obsolete entry.
5. End with a concise summary of the pass: how many entries scanned, how many merged, how many deleted. List the affected slugs.

Use Scope: global and the cron run's incident UUID for every memory-writer call. Do not touch any scope other than global.

If memory-searcher returns no entries or no clear duplicates, exit with a one-line "no-op: nothing to consolidate" summary.`,
		`You are running the nightly memory consolidation pass over the global cross-incident memory scope (/akmatori/memory/global/).

Goal: keep the global memory scope tight and high-signal. Concretely:

1. Use the memory-searcher subagent to list current entries — search broadly enough to surface duplicates, near-duplicates, and stale rows.
2. For each duplicate or near-duplicate cluster, decide which entry to keep (prefer the most specific, most recent, most operator-validated wording) and which to remove.
3. For each kept entry that should incorporate facts from a soon-to-be-deleted duplicate, prepare a merged body.
4. Apply mutations one at a time via the memory-writer subagent:
   - Action: upsert <slug> — write the merged or refreshed body (memory-writer is idempotent and overwrites by name).
   - Action: delete <slug> — remove a duplicate or obsolete entry.
5. End with a concise summary of the pass: how many entries scanned, how many merged, how many deleted. List the affected slugs.

Use Scope: global and the cron run's incident UUID for every memory-writer call. Do not touch any scope other than global.

If memory-searcher returns no entries or no clear duplicates, exit with a one-line "no-op: nothing to consolidate" summary.`,
	},
	improvementEvaluatorCronName: {
		`You are running the periodic self-improvement review. Goal: propose concrete changes that improve root-cause correctness and reduce analysis time for future investigations.

1. Gather recent history. List incidents started in the last 24 hours via gateway_call("incidents.list", {...}) — query status "monitor", "completed", and "failed" separately, using the "from" filter derived from the current-time header. List the cron runs of the same window via gateway_call("events.search", {"source": "cron", "since": ...}, "events"). Pick up to 10 incidents and cron runs for deep review, prioritizing: failed runs, the slowest execution_time_ms, and repeated titles. Fetch each incident with gateway_call("incidents.get", {"uuid": ...}) and each cron run with gateway_call("events.get", {"uuid": "<event uuid>"}, "events"), and study the full_log for wasted investigation steps, missing runbook coverage, wrong initial hypotheses, and tool errors.

2. Gather operator feedback. Ask the memory-searcher subagent for recent operator feedback (type: feedback) and recurring incident patterns relevant to what you saw in step 1.

3. Inspect current assets before proposing changes: read /akmatori/runbooks/ and /akmatori/memory/global/ directly, read /akmatori/skills/<name>/SKILL.md for any non-system skill you want to change, and call gateway_call("proposals.list_cron_jobs", {}) for current cron definitions. Never propose an update to something you have not read in its current form.

4. Dedup. Call gateway_call("proposals.list", {"status": "pending"}) and never re-propose a change that is already pending.

5. Emit at most 5 proposals via gateway_call("proposals.create", {...}). Choose the kind per proposal: runbook_new, runbook_update, memory_new, memory_update, cron_new, cron_update, or skill_prompt_update (non-system skills only). Each proposal needs: a title (imperative, under 80 chars), reasoning that cites the concrete incident UUIDs driving it, source_incident_uuids listing those UUIDs, target_ref for *_update kinds, and proposed_content in the documented JSON shape for the kind. Quality bar: only propose changes backed by at least 2 incidents or explicit operator feedback; prefer one high-confidence proposal over five speculative ones.

6. Final summary: one line per proposal created (title + kind), or "no-op: nothing actionable" when the review surfaced nothing worth proposing.`,
		`You are running the periodic self-improvement review. Goal: propose concrete changes that improve root-cause correctness and reduce analysis time for future investigations.

1. Gather recent history. List incidents started in the last 24 hours via gateway_call("incidents.list", {...}) — query status "monitor", "completed", and "failed" separately (source_kind "alert" and "cron"), using the "from" filter derived from the current-time header. Pick up to 10 incidents for deep review, prioritizing: failed runs, the slowest execution_time_ms, and repeated titles. Fetch each with gateway_call("incidents.get", {"uuid": ...}) and study the full_log for wasted investigation steps, missing runbook coverage, wrong initial hypotheses, and tool errors.

2. Gather operator feedback. Ask the memory-searcher subagent for recent operator feedback (type: feedback) and recurring incident patterns relevant to what you saw in step 1.

3. Inspect current assets before proposing changes: read /akmatori/runbooks/ and /akmatori/memory/global/ directly, read /akmatori/skills/<name>/SKILL.md for any non-system skill you want to change, and call gateway_call("proposals.list_cron_jobs", {}) for current cron definitions. Never propose an update to something you have not read in its current form.

4. Dedup. Call gateway_call("proposals.list", {"status": "pending"}) and never re-propose a change that is already pending.

5. Emit at most 5 proposals via gateway_call("proposals.create", {...}). Choose the kind per proposal: runbook_new, runbook_update, memory_new, memory_update, cron_new, cron_update, or skill_prompt_update (non-system skills only). Each proposal needs: a title (imperative, under 80 chars), reasoning that cites the concrete incident UUIDs driving it, source_incident_uuids listing those UUIDs, target_ref for *_update kinds, and proposed_content in the documented JSON shape for the kind. Quality bar: only propose changes backed by at least 2 incidents or explicit operator feedback; prefer one high-confidence proposal over five speculative ones.

6. Final summary: one line per proposal created (title + kind), or "no-op: nothing actionable" when the review surfaced nothing worth proposing.`,
	},
}
