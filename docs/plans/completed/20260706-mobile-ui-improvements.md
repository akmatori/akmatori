---
# Mobile UI Improvements

## Overview

Make the Akmatori frontend usable on mobile by adding a slide-in sidebar drawer with hamburger button, fixing content spacing and viewport height, making page headers responsive, and adding the missing table overflow on Proposals.

## Context

- Files involved:
  - `web/src/components/Layout.tsx` - main layout shell (sidebar + header)
  - `web/src/components/PageHeader.tsx` - per-page title + action row
  - `web/src/pages/Proposals.tsx` - only page with a table missing overflow-x-auto
- Related patterns: Tailwind responsive prefixes `md:`, `sm:`; existing `collapsed` sidebar state in Layout.tsx
- Dependencies: none (all Tailwind v4 utilities already available; `h-dvh` is built-in)

## Development Approach

- **Testing approach**: Regular (implement, then run tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Mobile sidebar overlay drawer

**Files:**
- Modify: `web/src/components/Layout.tsx`

The sidebar is currently always in the flex flow and never hides. On mobile, we need it to be a `position:fixed` overlay that slides in/out.

- [x] Add `mobileOpen` boolean state (default `false`)
- [x] Add useEffect to close sidebar on location.pathname change: `setMobileOpen(false)`
- [x] Change root container: `h-screen` → `h-dvh` (avoids iOS address-bar clipping)
- [x] Add semi-transparent backdrop: `<div className="fixed inset-0 z-30 bg-black/40 md:hidden" onClick={() => setMobileOpen(false)}>` rendered when `mobileOpen` is true
- [x] On the `<aside>`: add `fixed inset-y-0 left-0 z-40 md:static md:inset-auto` and drive translation with `${mobileOpen ? 'translate-x-0' : '-translate-x-full'} md:translate-x-0`; keep existing `transition-all duration-200` and width classes unchanged
- [x] Add `min-w-0` to `<main>` so flex content does not overflow on narrow screens
- [x] Add hamburger button to the top `<header>` bar: `<button className="block md:hidden p-2 ..." onClick={() => setMobileOpen(true)}><Menu size={20} /></button>` — placed as the first child of the header
- [x] Hide the desktop collapse toggle button on mobile: add `hidden md:flex` to the collapse toggle row in the sidebar footer
- [x] Add `onClick={() => setMobileOpen(false)}` to every `<Link>` in the nav (no-op on desktop, closes drawer on mobile)
- [x] Run `make test-web`

### Task 2: Content spacing and PageHeader responsiveness

**Files:**
- Modify: `web/src/components/Layout.tsx`
- Modify: `web/src/components/PageHeader.tsx`

- [x] Content padding: `p-6` → `p-3 sm:p-6` on the content wrapper div inside `<main>`
- [x] Header side padding: `px-6` → `px-4 md:px-6` on the top `<header>` bar
- [x] PageHeader: change outer `flex items-start justify-between` to `flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between`
- [x] PageHeader: title font size `text-2xl` → `text-xl sm:text-2xl`
- [x] PageHeader: remove `flex-shrink-0` from the action wrapper so it can wrap naturally
- [x] Run `make test-web`

### Task 3: Proposals table overflow

**Files:**
- Modify: `web/src/pages/Proposals.tsx`

- [x] Find the `<table>` element and wrap it in `<div className="overflow-x-auto">` (matching the pattern used in Incidents.tsx, Feed.tsx, and all other list pages)
- [x] Run `make test-web`

### Task 4: Sidebar footer touch targets

**Files:**
- Modify: `web/src/components/Layout.tsx`

The sidebar footer buttons (logout, theme toggle, collapse) use `p-1.5` (~28px tap target). Mobile needs ~44px minimum.

- [x] Change all `p-1.5` icon buttons in the sidebar footer section to `p-2.5` (applies to logout, theme toggle, and collapse buttons — 3 buttons total)
- [x] Run `make test-web`

### Task 5: Verify acceptance criteria

- [x] Run `make test-web`
- [x] Run `make verify`
