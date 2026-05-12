# Render parameter schemas in Assigned Tools for under-described tool types

## Overview

Add six new case clauses to `generateToolUsageExample` in `internal/services/skill_prompt_service.go` so that grafana, catchpoint, pagerduty, clickhouse, netbox, and kubernetes tool types render full parameter schemas and `gateway_call` usage examples in each skill's `## Assigned Tools` section. Today they fall through to the one-line default branch, which gives the agent no method list or schema hints — directly observed in incident cbba52c5 where the agent skipped Grafana entirely despite it being authorized.

## Context

- Files involved:
  - `internal/services/skill_prompt_service.go` — `generateToolUsageExample` (lines 310-451). Add six cases before the `default` branch at line 449.
  - `internal/services/skill_service_test.go` — extend the existing `## Assigned Tools` rendering tests (around lines 340-381) with table-driven coverage for the new types.
  - Reference for schemas: `mcp-gateway/internal/tools/registry.go` — `registerGrafanaTools` (~line 1960), `registerCatchpointTools`, `registerPagerDutyTools`, `registerClickHouseTools`, `registerNetBoxTools`, `registerK8sTools` registrations carry the canonical `Name`, required, and optional parameters per method.
- Related patterns: the four existing cases (ssh, zabbix, victoria_metrics, postgresql) define the byte-for-byte format — leading newline, `**Parameters:**` heading, dash-bulleted `method`: `required*` `|` `optional` lines, `(* = required)` footer, `Usage (via gateway_call):` heading, then a triple-backtick fenced block with `gateway_call(...)` examples using the `%s`-substituted `logicalName`.
- Default branch (line 449) stays as the safety net for any future tool type added without prompt support.
- Dependencies: none — the MCP gateway already registers full schemas for all six types; this is a prompt-rendering gap only.

## Development Approach

- Testing approach: Regular (code first, then tests). The change is mechanical schema rendering — write the six cases, then extend the existing `TestGenerateSkillMd_ListsAssignedTools`-style assertions to cover one representative method per new type.
- Complete each task fully before moving to the next.
- Keep the default branch intact.
- Do not change the prompt preamble at line 172 (the "no need to call `list_tools_for_tool_type`" line stays; once the default branch only fires for `http_connector` and future types, the claim is effectively accurate).
- CRITICAL: every task MUST include new/updated tests.
- CRITICAL: all tests must pass before starting next task.

## Implementation Steps

### Task 1: Add grafana and catchpoint cases

Files:
- Modify: `internal/services/skill_prompt_service.go`
- Modify: `internal/services/skill_service_test.go`

- [x] Add `case "grafana":` before the `default` branch, rendering the 13 grafana methods (search_dashboards, get_dashboard, get_dashboard_panels, get_alert_rules, get_alert_instances, get_alert_rule, silence_alert, list_data_sources, query_data_source, query_prometheus, query_loki, create_annotation, get_annotations) per the existing format
- [x] Add `case "catchpoint":` rendering the 12 catchpoint methods (get_alerts, get_alert_details, get_test_performance, get_test_performance_raw, get_tests, get_test_details, get_test_errors, get_internet_outages, get_nodes, get_node_alerts, acknowledge_alerts, run_instant_test)
- [x] Pick 6-8 representative `gateway_call(...)` examples per type for the fenced block (full method list lives in `**Parameters:**`)
- [x] Add or extend a table-driven test in `skill_service_test.go` asserting that for each new type, the rendered output contains the type name, at least one method name (e.g. `grafana.get_dashboard`, `catchpoint.get_alerts`), a required-param marker (`uid*`, `alert_ids*`), and a `gateway_call(...)` example using the logical name
- [x] Run `make test` and `go test ./internal/services/... -run SkillPrompt -count=1` — must pass before task 2

### Task 2: Add pagerduty and clickhouse cases

Files:
- Modify: `internal/services/skill_prompt_service.go`
- Modify: `internal/services/skill_service_test.go`

- [x] Add `case "pagerduty":` rendering the 13 pagerduty methods (get_incidents, get_incident, get_incident_notes, get_incident_alerts, get_services, get_on_calls, get_escalation_policies, list_recent_changes, acknowledge_incident, resolve_incident, reassign_incident, add_incident_note, send_event)
- [x] Add `case "clickhouse":` rendering the 10 clickhouse methods (execute_query, show_databases, show_tables, describe_table, get_query_log, get_running_queries, get_merges, get_replication_status, get_parts_info, get_cluster_info)
- [x] Pick 6-8 representative `gateway_call(...)` examples per type for the fenced block
- [x] Extend table-driven tests to cover one method assertion per new type (e.g. `pagerduty.acknowledge_incident`, `clickhouse.execute_query`)
- [x] Run `make test` and `go test ./internal/services/... -run SkillPrompt -count=1` — must pass before task 3

### Task 3: Add netbox and kubernetes cases

Files:
- Modify: `internal/services/skill_prompt_service.go`
- Modify: `internal/services/skill_service_test.go`

- [x] Add `case "netbox":` rendering all 19 netbox methods in `**Parameters:**` (DCIM get_devices/get_device/get_interfaces/get_sites/get_racks/get_cables/get_device_types, IPAM get_ip_addresses/get_prefixes/get_vlans/get_vrfs, get_circuits/get_providers, Virtualization get_virtual_machines/get_clusters/get_vm_interfaces, Tenancy get_tenants/get_tenant_groups, api_request); cap the fenced example block at 6-8 representative methods to stay readable
- [x] Add `case "kubernetes":` rendering the 17 kubernetes methods (get_namespaces, get_pods, get_pod_detail, get_pod_logs, get_events, get_deployments, get_deployment_detail, get_statefulsets, get_daemonsets, get_jobs, get_cronjobs, get_nodes, get_node_detail, get_services, get_configmaps, get_ingresses, api_request); pick 6-8 representative examples for the fenced block
- [x] Extend table-driven tests to cover one method assertion per new type (e.g. `netbox.get_devices`, `kubernetes.get_pods`, `namespace*`)
- [x] Run `make test` and `go test ./internal/services/... -run SkillPrompt -count=1` — must pass before task 4

### Task 4: Verify acceptance criteria

- [x] Run `make verify` (go vet + full Go test suite) — must be green
- [x] Run `make test-mcp` to confirm the gateway-side schemas are still aligned with what the prompt now claims
- [x] Manually inspect the rendered output for one skill of each new type by invoking the existing test path or a temporary test to print `generateSkillMd(...)`; confirm the `**Parameters:**` and `Usage (via gateway_call):` blocks match the four reference types byte-for-byte (leading newline, footer, fence placement) — covered by `TestGenerateToolUsageExample_NewToolTypes` assertions on `**Parameters:**`, `(* = required)`, and `Usage (via gateway_call):` markers; source review of lines 380-640 confirms identical fmt.Sprintf shape across reference and new cases
- [x] Confirm the `default` branch at line 449 still exists and is reachable for `http_connector` (and any future type) — branch lives at line 641 after the six new cases; reachability covered by `TestGenerateToolUsageExample_UnknownToolType`

### Task 5: Update documentation

- [ ] No CLAUDE.md update required — this change is internal to existing prompt-rendering and does not introduce new patterns or conventions
- [ ] Move this plan to `docs/plans/completed/`

## Post-Completion (manual, not agent-automated)

- After rebuilding the API container, trigger a skill resync (`SyncSkillFiles()` runs on startup) and `cat /akmatori/skills/grafana-watcher/SKILL.md` to confirm the new Assigned Tools content lands on disk.
- End-to-end repro of incident cbba52c5: post a fresh Slack channel message with the same Grafana dashboard URL and confirm at least one `gateway_call("grafana.…", …, "grafana")` invocation appears in the new incident's `full_log`.
- Out of scope for this change (tracked separately): improving `grafana-watcher/SKILL.md` body content with a short investigation strategy snippet — that is a content edit on the operator side, not a code change.
