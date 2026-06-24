# appmod examples

Runnable example applications that exercise every feature of the `appmod`
package. Each example is a self-contained `main` package; run it from the
repository root.

| Example | What it demonstrates |
| --- | --- |
| [`basic`](./basic) | The lifecycle of a single `BaseAppModule`: configuration, the four lifecycle hooks (`BeforeStart`/`AfterStart`/`BeforeDestroy`/`AfterDestroy`) and the state machine (`Created → Running → Destroyed`). |
| [`hooks`](./hooks) | Advanced hook features: the `New(...)` functional-options constructor, a structured `slog` logger, named/prioritized hooks via `AddHook`, removal via `RemoveHook`, the typed `*HookError`, and automatic rollback to `StateFailed` on a failing start hook. |
| [`manager`](./manager) | Orchestrating a dependency graph with `Manager`, plus inter-module communication: pull (request/response) via the shared `Registry` (`db` provides `DB`, `cache` requires `DB` and provides `Cache`, `api` requires both — `api → cache → db`) and push (fire-and-forget) via the shared `EventBus` (`api` publishes `UserCreated`, `cache` reacts), all sharing the `AppContext` the `Manager` injects. |

## Running

```sh
go run ./examples/basic
go run ./examples/hooks
go run ./examples/manager
```
