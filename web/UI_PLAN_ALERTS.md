# Alert Management UI Design Plan

## Overview

Extending the existing Akmatori frontend with alert source management and alerts viewer. The design maintains the existing clean corporate aesthetic while adding distinctive visual elements for the monitoring/alerting domain.

## Design Direction

**Aesthetic**: Industrial Monitoring Dashboard
- Emphasis on clear severity hierarchy with bold color-coding
- Real-time status indicators with subtle pulse animations
- Data-dense layouts optimized for quick scanning
- Copy-to-clipboard interactions for webhook URLs
- Expandable detail panels for deep investigation

**Color Severity System**:
- Critical: `#ef4444` (red-500) with red glow
- High: `#f97316` (orange-500)
- Warning: `#eab308` (yellow-500)
- Info: `#3b82f6` (blue-500)

**Status Indicators**:
- Firing: Pulsing red dot
- Resolved: Static green dot

---

## New Files to Create

### 1. Types (`/opt/akmatori/web/src/types/index.ts`)
Add new interfaces for alert system.

### 2. API Client (`/opt/akmatori/web/src/api/client.ts`)
Add alert source and alerts API functions.

### 3. Alert Sources Page (`/opt/akmatori/web/src/pages/AlertSources.tsx`)
Main management page for alert source instances.

### 4. Alerts Page (`/opt/akmatori/web/src/pages/Alerts.tsx`)
Alert history/viewer with filtering.

### 5. App Router (`/opt/akmatori/web/src/App.tsx`)
Add new routes.

### 6. Layout Navigation (`/opt/akmatori/web/src/components/Layout.tsx`)
Add new navigation items.

### 7. CSS Updates (`/opt/akmatori/web/src/index.css`)
Add severity badge styles and animations.

---

## Page Designs

### Alert Sources Page

```
┌─────────────────────────────────────────────────────────────────┐
│ Alert Sources                                    [+ New Source] │
│ Configure webhook integrations for monitoring systems           │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│ ┌─────────────────────────────────────────────────────────────┐ │
│ │ ● Production Alertmanager              [alertmanager] [ON]  │ │
│ │   Prometheus Alertmanager for production cluster            │ │
│ │                                                             │ │
│ │   Webhook URL: ████████████████████████████████  [📋 Copy]  │ │
│ │                                                             │ │
│ │   [View Settings ▼]                         [Edit] [Delete] │ │
│ └─────────────────────────────────────────────────────────────┘ │
│                                                                 │
│ ┌─────────────────────────────────────────────────────────────┐ │
│ │ ○ Staging Grafana                         [grafana] [OFF]   │ │
│ │   Grafana alerting for staging environment                  │ │
│ │   ...                                                       │ │
│ └─────────────────────────────────────────────────────────────┘ │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Create/Edit Form**:
- Source Type dropdown (from /api/alert-source-types)
- Instance Name
- Description
- Webhook Secret (password field)
- Field Mappings (JSON editor or key-value pairs)
- Settings (JSON)
- Enabled toggle

### Alerts Page

```
┌─────────────────────────────────────────────────────────────────┐
│ Alerts                                                          │
│ Recent alerts from all configured sources                       │
├─────────────────────────────────────────────────────────────────┤
│ Filters: [Source ▼] [Severity ▼] [Status ▼]     [🔄 Refresh]   │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│ 🔴 HighMemoryUsage                    CRITICAL   ● FIRING      │
│    web-server-01 | nginx              2 min ago                │
│    Memory usage exceeded 95% threshold                         │
│    [View Details]                                              │
│ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ │
│ 🟢 DiskSpaceWarning                   WARNING    ✓ RESOLVED    │
│    db-server-03 | postgresql          15 min ago               │
│    Disk usage above 80%                                        │
│    [View Details]                                              │
│ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ │
│                                                                 │
│ Showing 1-25 of 142                      [< Prev] [Next >]     │
└─────────────────────────────────────────────────────────────────┘
```

**Alert Detail Panel** (expandable):
```
┌─────────────────────────────────────────────────────────────────┐
│ HighMemoryUsage                                                 │
├─────────────────────────────────────────────────────────────────┤
│ Severity: CRITICAL      Status: FIRING      Source: prod-am    │
│ Started: 2024-01-15 14:32:05                                   │
├─────────────────────────────────────────────────────────────────┤
│ Summary:                                                        │
│ Memory usage on web-server-01 has exceeded 95% for 5 minutes   │
├─────────────────────────────────────────────────────────────────┤
│ Target:                                                         │
│   Host: web-server-01                                          │
│   Service: nginx                                               │
│   Labels: env=prod, team=platform                              │
├─────────────────────────────────────────────────────────────────┤
│ Metrics:                                                        │
│   Metric: node_memory_used_percent                             │
│   Value: 96.2%                                                 │
├─────────────────────────────────────────────────────────────────┤
│ [📖 View Runbook]  [🔗 View Incident]  [📋 Copy Raw JSON]      │
└─────────────────────────────────────────────────────────────────┘
```

---

## Component Breakdown

### AlertSourceCard
- Displays single alert source instance
- Shows webhook URL with copy button
- Expandable settings section
- Edit/Delete actions
- Status indicator (enabled/disabled)

### AlertRow
- Compact alert display for list
- Severity indicator (colored dot/icon)
- Status badge (firing/resolved)
- Relative time display
- Expandable for details

### AlertDetailPanel
- Full alert information
- Target labels as tags
- Raw payload viewer
- Links to runbook and incident

### SeverityBadge
- Color-coded badge component
- Used across alert displays

### StatusIndicator
- Pulsing dot for firing
- Static checkmark for resolved

### WebhookUrlDisplay
- Masked/revealed URL
- Copy to clipboard button
- Success toast on copy

---

## Implementation Order

1. Add types to `types/index.ts`
2. Add API functions to `api/client.ts`
3. Add CSS utilities for severity colors
4. Create AlertSources.tsx page
5. Create Alerts.tsx page
6. Update App.tsx with routes
7. Update Layout.tsx with navigation

---

## Navigation Updates

Add to sidebar after "Incidents":
- Alert Sources (Bell icon)
- Alerts (AlertTriangle icon)
