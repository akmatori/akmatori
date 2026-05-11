# Bump QMD from v2.1.0 → main HEAD (746beed)

## Overview

Bump the QMD search sidecar from the pinned v2.1.0 tag to upstream main HEAD (commit `746beedb4863524d337332109dc624a0be0b5aa7`). Primary motivation: incident 48f491d2 ("Italy EU-TTP CDN Outlier Alarm") surfaced QMD v2.1.0's overly-aggressive negation regex (`/-\w/`) which incorrectly flags legitimate hyphenated terms like "EU-TTP" as negation operators, causing vec/hyde queries to fail. Upstream commit d531211 fixes this on main but no tagged release includes it yet. Bumping to main HEAD also picks up the hybrid-RRF weighting fix (004714a), Node ESM normalization (656707c), case-preserving handelize (9fb9de4 + fee576b), and other unreleased fixes that affect our deployment pattern.

This is an infrastructure pin bump — only `qmd/Dockerfile` and `CLAUDE.md` change. No Go/TS source modifications. Verification is build-time + integration smoke against the bumped sidecar.

## Context

- Files involved:
  - `qmd/Dockerfile` (lines 7–9: version pin and clone command)
  - `CLAUDE.md` (QMD Search Service "Upstream patches" bullet around line 329)
- Related patterns:
  - Existing `sed` + `grep -q` verifier idiom in `qmd/Dockerfile` (lines 16–24) — keep unchanged
  - Standard shallow-clone-by-SHA pattern: `git init` + `remote add` + `fetch --depth 1 <sha>` + `checkout FETCH_HEAD`
- Dependencies:
  - Upstream commit `746beedb4863524d337332109dc624a0be0b5aa7` on github.com/tobi/qmd
  - Existing `qmd_cache` Docker volume needs a one-time reset on rollout per CLAUDE.md volume-note guidance
- Verified anchors at 746beed:
  - `httpServer.listen(port, "localhost"` still present at src/mcp/server.ts:817
  - `rerank: z.boolean().optional().default(true)` still present at src/mcp/server.ts:314

## Development Approach

- Testing approach: Regular — this is a dependency pin bump verified by build + integration smoke, not unit tests. No new Go/TS tests are added because no Akmatori source is modified; existing `make test-mcp` and `make test` suites are re-run to confirm no regression.
- Complete each task fully before moving to the next.
- The two existing sed patches must keep applying cleanly — their `grep -q` verifiers will fail the Docker build loudly if anchors drift, which is the project's idiom for catching upstream changes.
- CRITICAL: existing test suites must pass before declaring the bump done.
- CRITICAL: the `qmd_cache` volume must be reset once during rollout so baked-in model weights aren't shadowed by an empty pre-existing cache.

## Implementation Steps

### Task 1: Update qmd/Dockerfile to pin SHA via git init + shallow fetch

Files:
- Modify: `qmd/Dockerfile`

- [x] Replace `ARG QMD_VERSION=v2.1.0` with `ARG QMD_VERSION=746beedb4863524d337332109dc624a0be0b5aa7` and add a short comment explaining why the pin is a SHA rather than a tag (hyphen-validation fix d531211 and hybrid-RRF weighting fix 004714a not yet in any tagged release)
- [x] Replace `RUN git clone --branch ${QMD_VERSION} --depth 1 https://github.com/tobi/qmd.git /opt/qmd` with the `git init` + `remote add origin` + `fetch --depth 1 origin ${QMD_VERSION}` + `checkout FETCH_HEAD` sequence so a specific commit (not just a tag/branch) can be shallow-cloned
- [x] Leave lines 11–24 unchanged (bun.lock removal, npm install/build/link, both sed patches, both grep -q verifiers)
- [x] Build the image: `docker compose build qmd` — expect clone succeeds, npm install/build/link succeeds, both sed patches apply, both grep -q verifiers pass, precache-models.mjs runs, patch-server.js reports success

### Task 2: Update CLAUDE.md QMD Search Service section

Files:
- Modify: `CLAUDE.md` (QMD Search Service "Upstream patches" bullet around line 329)

- [x] Update the "Upstream patches" bullet to note that we now track a pinned main SHA rather than v2.1.0 (the two sed patches and their grep -q verifiers are unchanged in meaning)
- [x] Add a sibling bullet (or appended sentence) summarizing the inherited unreleased fixes brought in by the SHA pin: hyphen validation (d531211), hybrid-RRF weighting (004714a), Node 22 ESM normalization (656707c), handelize case preservation (9fb9de4 + fee576b). One short line — future readers need to know why the pin is a SHA, not why each fix matters
- [x] Keep the bullet concise to respect the CLAUDE.md size budget

### Task 3: Roll out the bumped sidecar and reset the embed cache

Files:
- No code modifications

- [x] `docker compose down qmd`
- [x] `docker volume rm akmatori_qmd_cache`
- [x] `docker compose up -d qmd`
- [x] Tail `docker compose logs -f qmd` until `qmd embed` finishes and the HTTP server is listening on `0.0.0.0:8181` (first start after bump may take several minutes — this is expected per the baked-models entrypoint) — embedded 176 chunks from 122 documents in 7m 30s; MCP HTTP server listening on port 8181; container healthy

### Task 4: Verify the negation-regex regression is fixed

Files:
- No code modifications

- [x] From inside `akmatori-mcp-gateway`, POST a `qmd.query` with a `vec`-type search whose query contains a hyphenated term (e.g., `"Italy EU-TTP CDN outlier alarm"`) against the runbooks collection
- [x] Confirm the response is a JSON-RPC result with a `results` array (possibly empty), NOT a JSON-RPC error containing `Negation (-term) is not supported in vec/hyde queries` — vec query returned `{"jsonrpc":"2.0","id":1,"result":{"content":[...]}}` with "Found 3 results"
- [x] Repeat once with a `hyde`-type search to cover the second code path the original bug hit — hyde query also returned a successful result with "Found 3 results"

### Task 5: Run existing Akmatori test suites

Files:
- No code modifications

- [x] `make test-mcp` — passes (gateway-side proxy + memory tests)
- [x] `make test` — passes (Go suite, including `internal/services/runbook_service_qmd_test.go`, `internal/database/prompt_test.go`, `internal/executor/executor_test.go`)
- [x] No new failures vs main; suites should be unaffected because no Go source changed, but a clean run confirms the contract with QMD's MCP surface still holds

### Task 6: Verify acceptance criteria

- [ ] Image builds cleanly with the new ARG and clone sequence
- [ ] Both sed patches still apply (build does not fail at the grep -q verifiers)
- [ ] qmd container reaches healthy state after cache reset
- [ ] Hyphenated vec/hyde queries no longer return the negation-regex error
- [ ] `make test-mcp` and `make test` pass with no new failures
- [ ] Run `go vet ./...` for sanity (no Go changes but cheap to confirm)

### Task 7: Update documentation and archive plan

- [ ] Confirm CLAUDE.md QMD Search Service section reflects the SHA pin and inherited fixes (Task 2)
- [ ] No README.md changes needed (no user-facing behavior change; runbook search just stops failing on hyphenated terms)
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion (out-of-band, not automatable)

- End-to-end smoke through the agent: trigger a synthetic incident whose alert text contains a hyphenated term (e.g., re-fire an "EU-TTP" alert via the Slack alert channel or webhook). Confirm in the agent's reasoning trace that the first `qmd.query` call returns results, not retries-with-quoted-phrases.
- Light-touch smoke for inherited fixes:
  - RRF weighting: re-run a previously-degraded query; confirm original FTS / original vector entries rank near the top of the fused list
  - Handelize case preservation: drop a `MyRunbook.md` (mixed-case) file, trigger `/update`, confirm `qmd.get` retrieves it at the original path
- Rollback: if Tasks 1–4 fail at any step, revert `qmd/Dockerfile` to `ARG QMD_VERSION=v2.1.0` + the original `git clone --branch ... --depth 1` line, rebuild, and reopen the question. No DB/volume migration is required to roll back — the `qmd_cache` volume is rebuildable from on-disk runbook/memory content.
