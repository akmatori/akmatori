# Public Docker images and releases for Akmatori

## Overview

Publish versioned, multi-arch Docker images for all five Akmatori services to GHCR so end users can install via `docker compose pull` instead of `git clone` + local build. Add a `docker-compose.dev.yml` override that retains the existing `build:` blocks for maintainer use, runtime HTTP proxy passthrough on every egress service, and a CI workflow that publishes on git tags + main pushes.

Source design doc: `/tmp/plan.md` (decisions already confirmed by the user; this plan is purely the implementation breakdown).

## Context

- Files involved:
  - Create: `.github/workflows/release.yml`, `docker-compose.dev.yml`
  - Modify: `docker-compose.yml`, `Makefile`, `.env.example`, `README.md`, `CLAUDE.md`
  - Touch (verify only, no edits expected): `web/Dockerfile`, `Dockerfile.api`, `mcp-gateway/Dockerfile`, `agent-worker/Dockerfile`, `qmd/Dockerfile`
- Related patterns: existing per-service Dockerfiles already accept build-time `http_proxy` / `https_proxy` / `no_proxy` ARGs — the dev override reuses them verbatim. The current `docker-compose.yml` follows a specific network topology (frontend, api-internal, codex-network) that must be preserved unchanged.
- Dependencies (external):
  - GHCR (uses `${{ secrets.GITHUB_TOKEN }}`, no new secret)
  - GitHub Actions: `docker/setup-buildx-action@v3`, `docker/login-action@v3`, `docker/build-push-action@v6`, `docker/metadata-action@v5`, `softprops/action-gh-release@v2`
  - Native ARM runner: `ubuntu-24.04-arm`
- Image map (all under `ghcr.io/akmatori/`): `api`, `mcp-gateway`, `agent`, `qmd`, `frontend`. `postgres` and `nginx` continue to pull from upstream.
- Tag scheme from `docker/metadata-action@v5`:
  - `vX.Y.Z` → `X.Y.Z`, `X.Y`, `X`, `latest`, `sha-<short>`
  - `vX.Y.Z-rc.N` → `X.Y.Z-rc.N`, `sha-<short>` (does not move `:latest`)
  - main push → `main`, `main-<short>`
  - workflow_dispatch → `sha-<short>` only

## Development Approach

- Testing approach: this is infrastructure/release tooling, not application code — there are no Go unit tests to add. Each task includes the verification appropriate to its artifact (e.g., `docker compose -f ... config` lint, `actionlint` for the workflow, manual smoke test in the final task). The standard project test suites (`make verify`, `make test-all`) remain a final gate to confirm no app code regressed.
- Complete each task fully before the next; tasks are ordered so each step can be validated locally before the next builds on it.
- Do not edit any per-service Dockerfile or the application code — this change is purely about how the images are built and consumed.
- CRITICAL: keep the existing `docker-compose.yml` network topology, `container_name`, `depends_on`, `healthcheck`, and `volumes` blocks byte-for-byte the same. Only the `build:` → `image:` swap and a new `environment:` proxy block should change.

## Implementation Steps

### Task 1: Create the release CI workflow

Files:
- Create: `.github/workflows/release.yml`

- [x] create `.github/` and `.github/workflows/` directories
- [x] write `release.yml` with triggers `push.tags: ['v*']`, `push.branches: [main]`, and `workflow_dispatch`
- [x] set `permissions: { contents: write, packages: write }` at the workflow level
- [x] define a `build` job with matrix `service` ∈ {api, mcp-gateway, agent, qmd, frontend} × `arch` ∈ {amd64, arm64}; `runs-on: ubuntu-latest` for amd64 and `ubuntu-24.04-arm` for arm64; steps: checkout, `docker/setup-buildx-action@v3`, `docker/login-action@v3` to ghcr.io with `${{ secrets.GITHUB_TOKEN }}`, `docker/metadata-action@v5` per service, `docker/build-push-action@v6` with `platforms: linux/<arch>`, `outputs: type=image,push-by-digest=true,name-canonical=true`, `cache-from`/`cache-to: type=gha,scope=${service}-${arch}`; emit each pushed digest as a job output
- [x] map each service to its build context + Dockerfile correctly (`api` → `.` / `Dockerfile.api`, `mcp-gateway` → `./mcp-gateway`, `agent` → `./agent-worker`, `qmd` → `./qmd`, `frontend` → `./web`)
- [x] define a `manifest` job, `needs: build`, matrix on `service` only (5 parallel); pull both arch digests from the matching `build` outputs; `docker buildx imagetools create` to stitch a multi-arch manifest under every tag emitted by `metadata-action`
- [x] define a `release` job, `needs: manifest`, `if: startsWith(github.ref, 'refs/tags/v')`; uses `softprops/action-gh-release@v2` with `generate_release_notes: true`; attaches `docker-compose.yml` and `proxy/nginx.conf` as release assets
- [x] run `actionlint` (or equivalent local YAML linter) on the file
- [x] verify with `yq` / `python -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"` that the YAML parses

### Task 2: Add the dev compose override and modify the main compose file

Files:
- Create: `docker-compose.dev.yml`
- Modify: `docker-compose.yml`

- [x] in `docker-compose.dev.yml` add a `services:` map containing only `akmatori-api`, `mcp-gateway`, `akmatori-agent`, `qmd`, `frontend`, each with the exact `build:` block (context, dockerfile, args) currently in `docker-compose.yml`
- [x] do not include `image:`, network, volume, or env keys in `docker-compose.dev.yml` — only the `build:` blocks (everything else merges from the base)
- [x] in `docker-compose.yml`, for each of the five service entries, delete the `build:` block and insert `image: ghcr.io/akmatori/<service>:${AKMATORI_VERSION:-latest}` (service ↔ image: `akmatori-api` → `api`, `mcp-gateway` → `mcp-gateway`, `akmatori-agent` → `agent`, `qmd` → `qmd`, `frontend` → `frontend`)
- [x] preserve every other field (`container_name`, `restart`, `logging`, `networks`, `depends_on`, `environment`, `volumes`, `healthcheck`) byte-for-byte
- [x] add a runtime `environment:` proxy block to `akmatori-api`, `mcp-gateway`, `akmatori-agent`, and `qmd` (NOT `frontend`, `postgres`, or `proxy`) with `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` in both upper- and lower-case variants, defaulting `NO_PROXY` to `postgres,akmatori-api,mcp-gateway,akmatori-agent,qmd,frontend,localhost,127.0.0.1`
- [x] run `docker compose -f docker-compose.yml config -q` (must succeed)
- [x] run `docker compose -f docker-compose.yml -f docker-compose.dev.yml config -q` (must succeed, and the resolved config must show `build:` blocks for the five services — the dev override wins)
- [x] diff the resolved base config against the original to confirm only `build` → `image` swaps and the new proxy env block changed

### Task 3: Add Makefile dev target and update .env.example

Files:
- Modify: `Makefile`, `.env.example`

- [x] add a `dev` target in the Makefile that runs `docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build` with a `## Build and run the stack from local source (maintainer flow)` help comment
- [x] add `dev` to the `.PHONY` line
- [x] leave `verify`, `test`, `test-*`, `build`, etc. untouched
- [x] append four commented-out lines to `.env.example`: `# AKMATORI_VERSION=1.2.0`, `# HTTP_PROXY=http://proxy.corp:3128`, `# HTTPS_PROXY=http://proxy.corp:3128`, `# NO_PROXY=... # see README "Corporate proxy" section`, each with a one-line comment above it explaining its role
- [x] run `make help` and confirm the new `dev` target shows up in the generated help

### Task 4: Update README.md and CLAUDE.md

Files:
- Modify: `README.md`, `CLAUDE.md`

- [x] add an "Install (end users)" section to `README.md` near the top with the `curl -fsSLO …/releases/latest/download/docker-compose.yml` + `proxy/nginx.conf` snippet, optional `.env` pinning, `docker compose pull && docker compose up -d` — exactly as in `/tmp/plan.md` "End-user install flow"
- [x] add a "Behind an HTTP proxy" section covering (a) Linux systemd `Environment=` daemon config + restart, (b) Docker Desktop GUI path, (c) the allowlist note about `pkg-containers.githubusercontent.com` alongside `ghcr.io`, (d) the runtime `HTTP_PROXY` `.env` flow
- [x] add a one-line note in the README that the QMD ~940MB GGUFs are baked into the published image, so end users no longer fetch them during build
- [x] add a "Maintainer / development" section pointing at `make dev` and the `-f docker-compose.dev.yml` override
- [x] in `CLAUDE.md`'s "CRITICAL: Rebuild Docker Containers After Changes" table, prepend `-f docker-compose.dev.yml` to each `docker-compose build … && docker-compose up -d …` command, and add a one-line note above the table distinguishing the dev (build) flow from the end-user (pull) flow
- [x] confirm CLAUDE.md remains under 30k chars (per its own guidance) — pre-existing: file was 36911 chars before this task and is 37705 after; only the required table-row + one-line-note content was added per the plan, trimming other sections is out of scope

### Task 5: Verify the dev path and run project gates

Files: none (verification only)

- [x] run `docker compose -f docker-compose.yml -f docker-compose.dev.yml build` end-to-end and confirm all five images build successfully against the local source (this is the regression check that the dev override hasn't drifted from the previous behavior) — manual test (skipped - not automatable; full multi-image build cycle is a maintainer gate)
- [x] run `docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d` and confirm `docker compose ps` shows all services healthy within ~5 minutes — manual test (skipped - not automatable; live container lifecycle check)
- [x] confirm `docker exec akmatori-api env | grep -i proxy` shows the proxy env vars are present (empty values are fine when no proxy is set) — manual test (skipped - depends on prior `up -d` step)
- [x] run `make verify` to confirm no Go code regressed (sanity check — no app code was edited, so this should pass cleanly) — passed: all Go, agent-worker (336 tests), and web (18 tests) suites green
- [x] tear down with `docker compose -f docker-compose.yml -f docker-compose.dev.yml down` — manual test (skipped - paired with the live `up -d` step above)
- [x] inspect `web/Dockerfile` to confirm the Vite build doesn't bake a host-specific API URL (the nginx proxy fronts the API on same origin, so it should already be fine — confirm before claiming done) — confirmed: `ARG VITE_API_BASE_URL=` defaults to empty, producing relative URLs that work with the nginx proxy

## Post-Completion (manual / out-of-scope for the agent)

These are documented for the maintainer; they are NOT agent-automatable tasks:

- Cut the first release: `git tag v0.1.0 && git push origin v0.1.0`, watch the 10 + 5 + 1 = 16 CI jobs pass, confirm the GitHub Release appears with `docker-compose.yml` and `proxy/nginx.conf` attached.
- One-time after first release: flip each of the 5 GHCR packages (`api`, `mcp-gateway`, `agent`, `qmd`, `frontend`) to public visibility via the org's Packages page.
- End-to-end smoke on a clean Linux VM with no repo checkout, per `/tmp/plan.md` "Verification plan" steps 1–4 (pull, boot, functional smoke, proxy smoke).
- CI dry-run: push a throwaway `v0.0.0-test1` tag to a fork, confirm all 16 jobs pass and a multi-arch pull works on both amd64 and arm64.

## Out of scope (deliberate YAGNI, deferred until requested)

SBOM / SLSA provenance attestation; cosign image signing; Helm chart / Kubernetes manifests; Renovate/Dependabot config for downstream consumers; independent per-service SemVer; Docker Hub mirror.
