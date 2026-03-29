# NetBox MCP Tool Integration

## Overview
Add a read-only NetBox tool to the MCP Gateway, providing full CMDB lookup capabilities across DCIM, IPAM, Circuits, Virtualization, and Tenancy modules. This enables the AI agent to correlate infrastructure context (devices, IPs, VMs, circuits, tenants) during incident investigation.

## Context
- Files involved:
  - `mcp-gateway/internal/tools/netbox/netbox.go` (new)
  - `mcp-gateway/internal/tools/netbox/netbox_test.go` (new)
  - `mcp-gateway/internal/tools/schemas.go` (modify)
  - `mcp-gateway/internal/tools/registry.go` (modify)
  - `mcp-gateway/internal/database/db.go` (modify - ProxySettings)
- Related patterns: Follow Catchpoint/PagerDuty tool implementation pattern
- Dependencies: NetBox REST API v3.x+ (Token auth via `Authorization: Token <token>`)

## NetBox API Authentication
NetBox uses `Authorization: Token <api_token>` header (not Bearer). The API base path is `/api/`.

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Follow existing Catchpoint/PagerDuty patterns exactly
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Tool Methods (All Read-Only)

### DCIM (Data Center Infrastructure Management)
| Tool | Endpoint | Purpose |
|------|----------|---------|
| `netbox.get_devices` | `GET /api/dcim/devices/` | List/search devices with filters |
| `netbox.get_device` | `GET /api/dcim/devices/{id}/` | Get device details by ID |
| `netbox.get_interfaces` | `GET /api/dcim/interfaces/` | List interfaces with filters |
| `netbox.get_sites` | `GET /api/dcim/sites/` | List sites |
| `netbox.get_racks` | `GET /api/dcim/racks/` | List racks with filters |
| `netbox.get_cables` | `GET /api/dcim/cables/` | List cable connections |
| `netbox.get_device_types` | `GET /api/dcim/device-types/` | List device types/models |

### IPAM (IP Address Management)
| Tool | Endpoint | Purpose |
|------|----------|---------|
| `netbox.get_ip_addresses` | `GET /api/ipam/ip-addresses/` | List/search IP addresses |
| `netbox.get_prefixes` | `GET /api/ipam/prefixes/` | List IP prefixes/subnets |
| `netbox.get_vlans` | `GET /api/ipam/vlans/` | List VLANs |
| `netbox.get_vrfs` | `GET /api/ipam/vrfs/` | List VRFs |

### Circuits
| Tool | Endpoint | Purpose |
|------|----------|---------|
| `netbox.get_circuits` | `GET /api/circuits/circuits/` | List circuits |
| `netbox.get_providers` | `GET /api/circuits/providers/` | List circuit providers |

### Virtualization
| Tool | Endpoint | Purpose |
|------|----------|---------|
| `netbox.get_virtual_machines` | `GET /api/virtualization/virtual-machines/` | List VMs |
| `netbox.get_clusters` | `GET /api/virtualization/clusters/` | List clusters |
| `netbox.get_vm_interfaces` | `GET /api/virtualization/interfaces/` | List VM interfaces |

### Tenancy
| Tool | Endpoint | Purpose |
|------|----------|---------|
| `netbox.get_tenants` | `GET /api/tenancy/tenants/` | List tenants |
| `netbox.get_tenant_groups` | `GET /api/tenancy/tenant-groups/` | List tenant groups |

### Generic
| Tool | Endpoint | Purpose |
|------|----------|---------|
| `netbox.api_request` | `GET /api/{path}` | Generic read-only API request for any endpoint |

## Cache TTLs
NetBox is a CMDB (mostly static data), so longer TTLs are appropriate:
- Config/credentials: 5 min (standard)
- Device/site/rack data: 60 sec
- IP/prefix data: 60 sec
- VM/cluster data: 60 sec
- Circuit/provider data: 120 sec (rarely changes)
- Tenant data: 120 sec (rarely changes)

## Implementation Steps

### Task 1: Add ProxySettings and Schema

**Files:**
- Modify: `mcp-gateway/internal/database/db.go`
- Modify: `mcp-gateway/internal/tools/schemas.go`

- [ ] Add `NetBoxEnabled bool` field to `ProxySettings` struct in `db.go` (with `gorm:"default:false"` tag)
- [ ] Add `getNetBoxSchema()` function in `schemas.go` with settings: `netbox_url` (required), `netbox_api_token` (required, secret), `netbox_verify_ssl` (advanced, default true), `netbox_timeout` (advanced, default 30)
- [ ] Register `"netbox": getNetBoxSchema()` in `GetToolSchemas()`
- [ ] Add all 21 tool functions to the schema's `Functions` slice
- [ ] Run `make test-mcp` - must pass before task 2

### Task 2: Core NetBox Tool Implementation

**Files:**
- Create: `mcp-gateway/internal/tools/netbox/netbox.go`

- [ ] Define `NetBoxConfig` struct (URL, APIToken, VerifySSL, Timeout, UseProxy, ProxyURL)
- [ ] Define `NetBoxTool` struct with logger, configCache (5min), responseCache (60sec), rateLimiter
- [ ] Implement `NewNetBoxTool(logger, limiter)` constructor
- [ ] Implement `Stop()` method for cache cleanup
- [ ] Implement `getConfig()` with credential resolution, proxy support, and caching
- [ ] Implement `getCachedProxySettings()` with NetBoxEnabled check
- [ ] Implement `doRequest()` with rate limiting, proxy, TLS, auth (`Token` header), and 5MB response limit
- [ ] Implement `cachedGet()` wrapper with response caching and TTL support
- [ ] Implement helper: `addPaginationParams()` (limit, offset)
- [ ] Implement helper: `addSearchParams()` for common NetBox filters (q, name, tag, tenant, site, region, role)
- [ ] Write basic constructor and config tests
- [ ] Run `make test-mcp` - must pass before task 3

### Task 3: DCIM Tool Methods

**Files:**
- Modify: `mcp-gateway/internal/tools/netbox/netbox.go`

- [ ] Implement `GetDevices()` - list/search with filters (name, site, role, status, tag, platform, tenant, q)
- [ ] Implement `GetDevice()` - single device by ID
- [ ] Implement `GetInterfaces()` - list with filters (device, device_id, name, type, enabled)
- [ ] Implement `GetSites()` - list with filters (name, region, status, tag, tenant, q)
- [ ] Implement `GetRacks()` - list with filters (site, name, status, role, tenant, q)
- [ ] Implement `GetCables()` - list with filters (device, site, type, status)
- [ ] Implement `GetDeviceTypes()` - list with filters (manufacturer, model, q)
- [ ] Write tests for all DCIM methods (mock HTTP server, test params, error cases)
- [ ] Run `make test-mcp` - must pass before task 4

### Task 4: IPAM Tool Methods

**Files:**
- Modify: `mcp-gateway/internal/tools/netbox/netbox.go`

- [ ] Implement `GetIPAddresses()` - list with filters (address, device, interface, vrf, tenant, status, q)
- [ ] Implement `GetPrefixes()` - list with filters (prefix, site, vrf, vlan, tenant, status, q)
- [ ] Implement `GetVLANs()` - list with filters (vid, name, site, group, tenant, q)
- [ ] Implement `GetVRFs()` - list with filters (name, tenant, q)
- [ ] Write tests for all IPAM methods
- [ ] Run `make test-mcp` - must pass before task 5

### Task 5: Circuits, Virtualization, and Tenancy Tool Methods

**Files:**
- Modify: `mcp-gateway/internal/tools/netbox/netbox.go`

- [ ] Implement `GetCircuits()` - list with filters (provider, type, status, tenant, q)
- [ ] Implement `GetProviders()` - list with filters (name, q)
- [ ] Implement `GetVirtualMachines()` - list with filters (name, cluster, site, status, role, tenant, q)
- [ ] Implement `GetClusters()` - list with filters (name, type, group, site, tenant, q)
- [ ] Implement `GetVMInterfaces()` - list with filters (virtual_machine, name, enabled)
- [ ] Implement `GetTenants()` - list with filters (name, group, q)
- [ ] Implement `GetTenantGroups()` - list with filters (name, q)
- [ ] Implement `APIRequest()` - generic GET to any `/api/{path}` with optional query params
- [ ] Write tests for all methods in this task
- [ ] Run `make test-mcp` - must pass before task 6

### Task 6: Registry Integration

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go`

- [ ] Add rate limiter constants: `NetBoxRatePerSecond = 10`, `NetBoxBurstCapacity = 20`
- [ ] Add `netboxTool *netbox.NetBoxTool` and `netboxLimit *ratelimit.Limiter` fields to Registry struct
- [ ] Add `r.netboxLimit = ratelimit.New(...)` and `r.registerNetBoxTools()` in `RegisterAllTools()`
- [ ] Add `r.netboxTool.Stop()` in the registry `Stop()` method
- [ ] Implement `registerNetBoxTools()` - register all 21 tools with proper InputSchema definitions
- [ ] Run `make test-mcp` - must pass before task 7

### Task 7: Verify Acceptance Criteria

- [ ] Run full test suite: `make test-mcp`
- [ ] Run linter: `cd mcp-gateway && go vet ./...`
- [ ] Verify test coverage for `mcp-gateway/internal/tools/netbox/` meets 80%+
- [ ] Manual verification: confirm schema appears in `GetToolSchemas()` output
- [ ] Verify frontend dynamically renders NetBox config form (no frontend changes needed - dynamic schema)

### Task 8: Update Documentation

- [ ] Update CLAUDE.md: add NetBox to the tool list in "Implementation Reference" section
- [ ] Update CLAUDE.md: add NetBox patterns note alongside Catchpoint/PagerDuty patterns
- [ ] Move this plan to `docs/plans/completed/`
