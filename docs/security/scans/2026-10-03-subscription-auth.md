# Subscription authentication validation

Scope: Codex subscription configuration, worker authentication, one-shot callers,
and settings UI. No new package or lockfile changes. No production services were
started, no subscription credentials were used, and no paid inference was run.

## Executed checks

- `make verify`: passed Go vet/tests for API and MCP Gateway; 459 worker tests
  and 178 frontend tests passed.
- Worker TypeScript build and frontend production build: passed.
- Docker Compose development builds for `akmatori-api`, `akmatori-agent`, and
  `frontend`: passed. Services were not deployed.
- Worker image smoke check with networking disabled: verified the bundled Codex
  catalog and explicit rejection of a one-shot request without subscription login.
- Frontend ESLint: 52 errors and 8 warnings. Compared rule, severity, file, and
  count against an isolated `11732e4` source snapshot using the same dependencies:
  **no added diagnostics**. Existing lint failures remain outside this change.
- `git diff --check`: passed. Modified Go files formatted with `gofmt`.
- OpenAPI YAML parsed successfully after updating the settings endpoints.
- Gitleaks 8.30.1: no secrets found in a snapshot of changed and new files. The
  official release binary was checksum-verified and run from `/tmp`.
- `npm audit --omit=dev`: worker has zero reported vulnerabilities; frontend has
  two high findings (`react-router`, inherited by `react-router-dom`). No package
  versions changed in this contribution.

The first sandboxed test runs could not bind localhost sockets. Tests passed
when rerun with local networking enabled. Earlier failures from provider-count
fixtures and missing OAuth methods in SDK test doubles were corrected before
the successful full verification.

## Remaining validation

- Live OAuth sign-in, refresh/revocation, quota exhaustion, and investigation with
  real child agents require an operator-owned subscription account. Unit tests
  cover missing credentials, failed credential resolution, and rejection of API
  keys/custom URLs with external SDK calls mocked. No live subscription success
  is claimed.
- Semgrep and Trivy were not installed and were not run. Go vet, compilation,
  tests, dependency audit, and the scoped secret scan do not replace those scans.
- Authentication storage uses the existing pi volume. Encryption at rest and
  backup access are deployment responsibilities; the worker identity and its
  tools can access the same credentials. There is no new tenant isolation.

## Existing dependency findings

React Router advisories cover several server/RSC paths as well as navigation
redirect behavior. This frontend is a client-side SPA, so the audit findings do
not all imply reachable paths; exploitability was not established here. Keep
the findings open and review a separate patched-version update before production
rollout. Review target: 2026-10-10.

Representative upstream advisories:

- [Navigation open redirect](https://github.com/advisories/GHSA-wrjc-x8rr-h8h6)
- [Route matching denial of service](https://github.com/advisories/GHSA-chx6-hx7r-mcp5)
- [Server-side hydration constructor injection](https://github.com/advisories/GHSA-337j-9hxr-rhxg)

Claude Code and Gemini CLI subscription engines remain outside this implementation;
see [subscription support](../../SUBSCRIPTIONS.md) and
[issue #29](https://github.com/akmatori/akmatori/issues/29).
