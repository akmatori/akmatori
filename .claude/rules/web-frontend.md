---
paths:
  - "**/web/src/**"
---

# Web frontend (React 19 + TS + Vite + Tailwind)

- gate: `npx tsc -b && npx eslint . && npx vitest run` in `web/` must all be clean — the lint
  config is strict (`no-explicit-any`, react-hooks v7 incl. `set-state-in-effect` and `purity`,
  `react-refresh/only-export-components`)
- no `any`: free-form JSON is `Record<string, unknown>`, narrowed where read
- errors: `getErrorMessage(err, fallback)` from `utils/errors.ts`; never `err.message` on unknown
- formatting: relative/absolute times, countdowns, durations, tokens and file sizes come from
  `utils/format.ts` — do not add page-local copies
- timers: `useInterval(cb, delayMs | null)` (`hooks/useInterval.ts`); the callback is read via a
  ref so polling does not restart on unrelated state changes. Mount-once effects that call a
  `useCallback` read it through `useLatestRef`
- reset-on-prop-change: compare against the last seen value in state during render, do not
  `setState` synchronously inside `useEffect`
- context: object in `context/<name>-context.ts`, hook in `context/use<Name>.ts`, provider in
  `context/<Name>Context.tsx` — provider files export components only
- shared UI: `components/shared/` (EmptyState, Pagination, SegmentedControl, SourceKindChip) —
  reuse before adding bespoke markup; incident status badges come from
  `components/incidents/incidentStatus.ts`
- pages wire hooks + components and hold page state; extracted feature components live in
  `components/<area>/`, pure helpers next to them as `*Helpers.ts` with a colocated test
- close-incident flow (unforced attempt → 409 → `CloseIncidentModal`) is `hooks/useCloseIncident.ts`
