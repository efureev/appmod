# appmod examples

Runnable example applications that exercise every feature of the `appmod`
package. Each example is a self-contained `main` package; run it from the
repository root.

| Example | What it demonstrates |
| --- | --- |
| [`basic`](./basic) | The lifecycle of a single `BaseAppModule`: configuration, the four lifecycle hooks (`BeforeStart`/`AfterStart`/`BeforeDestroy`/`AfterDestroy`) and the state machine (`Created → Running → Destroyed`). |
| [`hooks`](./hooks) | Advanced hook features: the `New(...)` functional-options constructor, a structured `slog` logger, named/prioritized hooks via `AddHook`, removal via `RemoveHook`, the typed `*HookError`, and automatic rollback to `StateFailed` on a failing start hook. |
| [`manager`](./manager) | Orchestrating a dependency graph with `Manager`: a custom module that embeds `BaseAppModule` and implements `HealthChecker`, topological start / reverse stop, concurrent start of independent modules, `Health` probing and graceful shutdown via `Run`. |

## Running

```sh
go run ./examples/basic
go run ./examples/hooks
go run ./examples/manager
```
