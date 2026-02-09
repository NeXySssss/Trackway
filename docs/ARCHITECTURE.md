# Trackway Architecture

## 1. Design Goals
- Keep operational behavior deterministic and testable.
- Isolate state machine logic from transport adapters.
- Allow replacing Telegram, dashboard, and storage independently.
- Keep runtime wiring explicit in a single place.

## 2. Module Layout
```
cmd/trackway/main.go
internal/config
internal/logstore
internal/telegram
internal/dashboard
internal/tracker
  - engine.go      // monitoring loop + state transitions + snapshot/query
  - alerts.go      // alert batching/editing strategy, notifier side effects
  - commands.go    // telegram command handler and rendering
  - service.go     // composition/facade for the app runtime
  - types.go       // shared contracts and domain structs
```

## 3. Runtime Composition
1. `main` loads config and creates adapters (`logstore`, `telegram`, `dashboard`).
2. `tracker.New(...)` builds:
   - `MonitorEngine`
   - `AlertManager`
   - `CommandHandler`
3. Monitor ticks in `RunMonitor`:
   - `MonitorEngine` probes targets and emits transition events.
   - `AlertManager` consumes events and sends grouped notifications.
4. Telegram updates go to `CommandHandler`.
5. Dashboard reads state/log data via `Service` query methods.

Concrete runtime adapters:
- Logs: `logstore.NewSQLite(...)` in production (memory backend for tests).
- Dashboard: Go `net/http` serves embedded Astro `frontend/dist` assets.
- Auth: one-time `/authme` token -> session cookie; optional Telegram Mini App auto-auth.

## 4. Responsibilities
### `MonitorEngine`
- Owns in-memory target state.
- Syncs target definitions from storage (`track_targets`) before checks.
- Executes checks and writes log rows (`INIT`, `CHANGE`, `POLL`).
- Exposes read model: `Snapshot()` and `Logs(...)`.

### `AlertManager`
- Contains alert grouping and fast-recovery edit logic.
- Stores pending alert message metadata.
- Uses `Notifier` only for outbound side effects.

### `CommandHandler`
- Parses bot commands.
- Renders bot responses (`/list`, `/status`, `/logs`, `/authme`).
- Holds auth-link function without touching monitor internals.

### `Service` (facade)
- Public app boundary used by `main` and dashboard wiring.
- Delegates to internal components without embedding business rules.

### `logstore`
- Owns storage backend contract (`append`, `readSince`).
- SQLite backend is production default (small footprint, local file).
- Keeps read model stable (`[]Row`) so tracker/dashboard stay storage-agnostic.

### `dashboard`
- Owns browser auth/session lifecycle and API handlers.
- Verifies Telegram Mini App `initData` server-side.
- Serves static UI from embedded Astro build output.

## 5. Dependency Direction
- `tracker/engine` depends on `config` + `logstore`.
- `tracker/alerts` depends on `Notifier` contract only.
- `tracker/commands` depends on `Notifier` + `QueryProvider` contracts.
- `dashboard` depends on query methods (`Snapshot`, `Logs`) only.
- `main` is the only place that knows concrete adapter types.

No adapter should import another adapter directly.

## 6. Extension Rules
- New transport (e.g. HTTP webhook bot): add adapter + wire in `main`, do not change `engine`.
- New storage backend: keep `logstore` API shape or add interface wrapper at composition layer.
- New bot command: implement in `commands.go`; avoid touching alert/monitor modules.
- New alert policy: implement inside `alerts.go`; no command/dashboard changes required.

## 7. Frontend Build Pipeline
1. Develop UI in `internal/dashboard/frontend/src`.
2. Build with `npm run build` -> `internal/dashboard/frontend/dist`.
3. Go binary embeds `frontend/dist` via `go:embed`.

Production does not require Node runtime. Node is only needed at build time.
