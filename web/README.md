# Akmatori web UI

React 19 + TypeScript + Vite + Tailwind. This is the operator console served by the
`frontend` container; it talks to the Go API under `/api` and to the MCP gateway under `/mcp`.

## Commands

```bash
npm run dev       # Vite dev server on :5173, /api proxied (see vite.config.ts)
npm run build     # tsc -b && vite build  → dist/
npm run lint      # eslint (must report 0 problems)
npm test          # vitest (jsdom)
```

From the repo root, `make test-web` runs the tests.

## Layout

```text
src/
  api/client.ts          fetch wrapper, ApiError, one `xxxApi` object per resource
  types/index.ts         API types (mirror docs/openapi.yaml)
  utils/                 pure helpers: errors.ts (getErrorMessage), format.ts (dates, durations, sizes)
  hooks/                 reusable hooks: useInterval, useLatestRef, useCloseIncident, use*Management
  context/               AuthProvider / ThemeProvider (+ their hooks in useAuth.ts / useTheme.ts)
  components/shared/     EmptyState, Pagination, SegmentedControl, SourceKindChip
  components/<area>/     feature components: feed/, incidents/, alerts/, channels/, cron/,
                         proposals/, settings/, tools/  (pure helpers live next to them as *Helpers.ts)
  pages/                 one component per route; wires hooks + components, holds page state
  App.tsx                routes
```

## Conventions

- **No `any`.** Free-form JSON is `Record<string, unknown>`; narrow at the point of use.
- **Error text goes through `getErrorMessage(err, fallback)`.** Never `err.message` on an unknown.
- **Display formatting goes through `utils/format.ts`.** Do not re-implement relative times,
  durations or token counts inside a page.
- **Timers go through `useInterval`.** It reads the callback through a ref, so list-refresh and
  polling intervals do not restart when page state changes.
- **Reset-on-prop-change happens during render**, not in an effect (compare against the last seen
  value in state), so the lint rule `react-hooks/set-state-in-effect` stays green.
- **Context modules only export components.** The context object lives in `*-context.ts`, the
  hook in `use*.ts`, the provider in `*Context.tsx` (required by `react-refresh/only-export-components`).
- **Pure logic is tested.** Helpers (`*Helpers.ts`, `incidentStatus.ts`, `utils/*`) get a
  colocated `*.test.ts`; components get a render test only when they carry behavior.
