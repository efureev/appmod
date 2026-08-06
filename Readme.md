# appmod — Abstract Application Module

[English](Readme.md) | [Русский](Readme.ru.md)

[![Test](https://github.com/efureev/appmod/actions/workflows/test.yml/badge.svg)](https://github.com/efureev/appmod/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/efureev/appmod)](https://goreportcard.com/report/github.com/efureev/appmod)
[![codecov](https://codecov.io/gh/efureev/appmod/branch/master/graph/badge.svg)](https://codecov.io/gh/efureev/appmod)
[![Go Reference](https://pkg.go.dev/badge/github.com/efureev/appmod.svg)](https://pkg.go.dev/github.com/efureev/appmod)
[![License](https://img.shields.io/github/license/efureev/appmod)](LICENSE)

A tiny, dependency-free building block for structuring an application as a set of
**modules** with a common, context-aware lifecycle (`Init` / `Destroy`) and lifecycle
hooks (`BeforeStart` / `AfterStart` / `BeforeDestroy` / `AfterDestroy`).

## Features

- Minimalistic and dependency-free.
- Clear separation between the **contract** (interfaces) and the **base implementation**.
- Context-aware lifecycle: `Init(ctx)` / `Destroy(ctx)`.
- Four sets of lifecycle hooks; multiple hooks can be registered per phase and run in order.
- **Named, prioritized and removable hooks** (`Hook`, `AddHook` / `RemoveHook`): within a phase, hooks run in ascending priority order.
- Hooks receive a narrow, read-only `HookModule` view (config/name/state) instead of the full module — an opaque value, so it cannot be asserted back to `Lifecycle` or `HookRegistry`.
- Hooks can abort startup/shutdown by returning an `error`, reported as a typed `HookError` (phase, index, name, module).
- Optional **per-module structured logging** (`slog`) of lifecycle transitions and phase durations.
- Idempotency guard: double `Init` or `Destroy` before `Init` returns a sentinel error.
- Explicit lifecycle **state machine** (`Created → Initializing → Running → Destroying → Destroyed`, plus `Failed`) exposed via `State()`.
- **Context-aware** lifecycle: hooks are skipped once the context is canceled.
- **Atomic `Init`**: any start-hook failure (or context cancellation) triggers an automatic rollback (the `AddCleanup` compensations are unwound in reverse) and leaves the module in `StateFailed`.
- Embeddable `BaseAppModule` — implement your own module by embedding it.
- **Concurrency-safe**: lifecycle, hook registration and config access are mutex-guarded.
- **Panic-safe hooks**: a panic in a hook is recovered and returned as an error.
- Narrow capability interfaces (`Configurable` / `Named` / `Stateful` / `Lifecycle` / `HookRegistry`) composed into `AppModule`.
- `New(opts ...Option)` constructor with functional options.
- **Shutdown observation** (`AppContext.Done()`): a module reacts to the shutdown as it starts, without blocking and without waiting for its own `Destroy`.
- **Module orchestrator** `Manager`: dependency-ordered (topological) start with concurrent start of independent modules, layered reverse-order stop that is likewise concurrent within a layer, dependency-cycle detection, `SIGINT`/`SIGTERM`-aware graceful shutdown and optional health checks.
- **Lifecycle-scoped compensations** (`AddCleanup`, `SubscribeModule`): the release is registered next to the acquisition and runs on `Destroy` and on rollback.

## Requirements

- Go **1.24** or newer.

## Install

```bash
go get github.com/efureev/appmod/v3
```

## API Overview

```go
// AppModuleConfig describes module configuration.
type AppModuleConfig interface {
    Name() string
    Version() string
}

// HookFunc is a lifecycle hook; it receives a narrow, read-only view.
type HookFunc func(ctx context.Context, mod HookModule) error

// Narrow capability interfaces.
type Configurable interface {
    SetConfig(config AppModuleConfig)
    Config() AppModuleConfig
}

type Named interface {
    Name() string
}

type Stateful interface {
    State() State
}

// HookModule is the narrow, read-only view passed to a HookFunc.
type HookModule interface {
    Configurable
    Named
    Stateful
}

type Lifecycle interface {
    Init(ctx context.Context) error
    Destroy(ctx context.Context) error
}

type HookRegistry interface {
    BeforeStart(fn HookFunc)
    AfterStart(fn HookFunc)
    BeforeDestroy(fn HookFunc)
    AfterDestroy(fn HookFunc)
    AddHook(phase Phase, hook Hook)
    RemoveHook(phase Phase, name string) bool
}

// AppModule is composed of the narrow interfaces above.
type AppModule interface {
    Configurable
    Named
    Stateful
    Lifecycle
    HookRegistry
}
```

`BaseAppModule` is safe for concurrent use by multiple goroutines, and a panic
raised inside a hook is recovered and returned as an error.

The lifecycle is an explicit state machine exposed through `State()`:

```
Created → Initializing → Running → Destroying → Destroyed
```

Calling `Init` while the module is running returns `ErrAlreadyInitialized`;
calling `Destroy` on a module that is not running returns `ErrNotInitialized`.
A destroyed (or failed) module can be initialized again.

The module reaches `StateRunning` only once `Init` has completed **both** start
phases, so an `AfterStart` hook still observes `StateInitializing`. This is
deliberate: publishing `StateRunning` before the module has finished starting
would let a concurrent `Destroy` pass its state guard and tear the module down
mid-startup, running the teardown hooks twice.

Teardown hooks always observe `StateDestroying`, whether they were reached
through `Destroy` or through `Init`'s rollback, so a hook may branch on
`State()` without caring how it was invoked.

The zero value is genuinely ready to use: `Config()` reports `DefaultConfig()`
until `SetConfig` is called, and `Name()` follows it exactly, so
`m.Config().Name()` inside a hook is always safe. An unconfigured module is
therefore named `App Module`.

`Init` is **atomic**: if any start hook (`BeforeStart` or `AfterStart`) returns
an error, or the context is canceled, the module automatically rolls back and
ends up in `StateFailed`. Rollback errors are joined with the original cause via
`errors.Join`. The module is therefore never left half-started: `Init` either
fully succeeds (`StateRunning`) or fails (`StateFailed`).

The rollback unwinds the cleanups registered with `AddCleanup`, in reverse
order — **not** the teardown hooks. Teardown hooks describe how to stop a module
that *finished* starting; running them after a half-finished start would close a
pool that was never opened. Register the release next to the acquisition and a
hook that never ran leaves nothing to undo:

```go
mod.BeforeStart(func(ctx context.Context, m appmod.HookModule) error {
    pool, err := openPool()
    if err != nil {
        return err
    }
    mod.AddCleanup(func(context.Context) error { return pool.Close() })

    return nil
})
```

Cleanups also run on a normal `Destroy`, between the `BeforeDestroy` and
`AfterDestroy` phases.

### Constructors

| Function                       | Description                                   |
|--------------------------------|-----------------------------------------------|
| `NewConfig(name, version)`     | Creates a `Config` with the given name/version. |
| `DefaultConfig()`              | Returns a default `Config` (`App Module`, `v0.0.1`). |
| `New(opts ...Option)`          | Creates a `*BaseAppModule` configured with functional options. |

Functional options: `WithConfig`, `WithModuleLogger`, `WithHook`,
`WithBeforeStart`, `WithAfterStart`, `WithBeforeDestroy`, `WithAfterDestroy`.

### Named, prioritized hooks

Beyond the anonymous `BeforeStart` / `AfterStart` / ... helpers, hooks can be
registered with a name and a priority and removed later. Within a phase, hooks
run in ascending priority order (ties keep registration order):

```go
mod.AddHook(appmod.PhaseBeforeStart, appmod.Hook{
    Name:     "open-db",
    Priority: -10, // runs early
    Run: func(ctx context.Context, m appmod.HookModule) error { return nil },
})
mod.RemoveHook(appmod.PhaseBeforeStart, "open-db")
```

A failing hook is returned as a `*HookError` carrying the phase, index, hook
name and module name; it unwraps to the original error so `errors.Is` /
`errors.As` keep working.

`AddHook`, `RemoveHook` and `WithHook` **panic** if the phase is not one of the
four defined values. An out-of-range `Phase` is a programmer error, and the
alternative — dropping the hook silently — turns start-up logic into code that
never runs, with nothing to notice. Check a phase from configuration or a wire
format with `Phase.Valid()` first.

### Per-module logging

Attach an `*slog.Logger` to a module (via `WithModuleLogger` or `SetLogger`) to
get structured logs of lifecycle transitions and phase durations. Logging
defaults to a no-op handler.

```go
mod := appmod.New(
    appmod.WithConfig(appmod.NewConfig("Cache", "v1.0.0")),
    appmod.WithBeforeStart(func(ctx context.Context, m appmod.AppModule) error {
        return nil
    }),
)
```

## Usage

### Basic

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/efureev/appmod/v3"
)

func main() {
	ctx := context.Background()

	mod := &appmod.BaseAppModule{}
	mod.SetConfig(appmod.NewConfig("My Module", "v1.0.0"))

	// Register lifecycle hooks.
	mod.BeforeStart(func(ctx context.Context, m appmod.AppModule) error {
		fmt.Printf("starting %s %s\n", m.Config().Name(), m.Config().Version())
		return nil
	})
	mod.BeforeDestroy(func(ctx context.Context, m appmod.AppModule) error {
		fmt.Printf("stopping %s\n", m.Config().Name())
		return nil
	})

	if err := mod.Init(ctx); err != nil {
		log.Fatalf("init failed: %v", err)
	}
	defer func() {
		if err := mod.Destroy(ctx); err != nil {
			log.Fatalf("destroy failed: %v", err)
		}
	}()

	// ... application logic ...
}

```

### Custom module by embedding

```go
type CacheModule struct {
    appmod.BaseAppModule
    // your own fields...
}

func NewCacheModule() *CacheModule {
    m := &CacheModule{}
    m.SetConfig(appmod.NewConfig("Cache", "v1.0.0"))
    return m
}
```

### Aborting startup

If a `BeforeStart` hook returns an error, `Init(ctx)` returns it (wrapped) and the
module is considered not started:

```go
mod.BeforeStart(func(ctx context.Context, m appmod.AppModule) error {
    return fmt.Errorf("config is invalid")
})

if err := mod.Init(ctx); err != nil {
    // handle the error
}
```

The same applies to `BeforeDestroy` and `Destroy(ctx)`.

### Orchestrating modules

For an application composed of several inter-dependent modules, `Manager` starts
them in dependency (topological) order — independent modules concurrently — and
stops them in the reverse order:

```go
mgr := appmod.NewManager(
    appmod.WithShutdownTimeout(10*time.Second),
)
_ = mgr.Register("db", db)
_ = mgr.Register("cache", cache, "db")        // cache depends on db
_ = mgr.Register("api", api, "cache", "db")   // api depends on both

// Start, wait for SIGINT/SIGTERM, then gracefully stop in reverse order.
if err := mgr.Run(context.Background()); err != nil {
    log.Fatal(err)
}
```

`Register(name, module, deps...)` validates names and dependencies; `Start`
returns `ErrUnknownDependency` for missing dependencies and `ErrDependencyCycle`
if the graph has a cycle. A failed `Start` rolls back the modules that already
started. Modules implementing `HealthChecker` can be probed via `mgr.Health(ctx)`.

Teardown mirrors startup: `Stop` reuses the same dependency layers, walks them in
reverse and tears down each layer **concurrently**, so shutdown costs the maximum
teardown per layer rather than the sum over all modules — which is what used to
overrun a `WithShutdownTimeout` budget on an application of mostly independent
modules. Ordering between layers is unchanged: a module is always stopped before
the modules it depends on.

Every module can observe the shutdown as soon as it begins, through the
`AppContext` the manager injected — useful for a background loop that should
stop taking new work before its layer is torn down:

```go
go func() {
    for {
        select {
        case <-m.AppContext().Done():
            return // shutdown started: stop taking new work
        case job := <-jobs:
            process(job)
        }
    }
}()
```

`Done()` closes for any teardown the manager performs, including the rollback of
a failed `Start`. `AppContext.Context()` gives the same signal as a context, safe
to derive request-scoped contexts from.

`Run` waits for `SIGINT`, `SIGTERM` or `SIGQUIT` by default (`WithSignals`
replaces the set), for the context to be canceled, or for `Manager.Shutdown()`.
A **second signal while the teardown is running terminates the process** with
exit code 128+signum — the operator's way out of a teardown that hung, since the
signal handler is installed and a second Ctrl-C would otherwise go nowhere.
Disable it with `WithForceOnSecondSignal(false)`, and note that this leaves
SIGKILL as the only way to stop a module that ignores its context.
`Manager.ExitCode()` reports the matching exit code for `os.Exit`.

`Run` is **single use**: the shutdown sequence runs exactly once, so a second
call returns `ErrAlreadyRun`. A manager driven through `Start`/`Stop` can still
be restarted.

`Start` is **not re-entrant**: calling it on a manager that is already starting
or running returns `ErrAlreadyStarted` and leaves the running modules untouched.
The guard is what makes a stray or concurrent second `Start` harmless — without
it the second call would fail on the already-initialized modules, treat that as
a startup failure, and roll back, stopping the modules the first `Start` had
successfully brought up. After `Stop` the manager can be started again.

`Run` delegates its graceful-shutdown sequence (signal handling and the
timeout-bounded teardown) to
[`github.com/efureev/go-shutdown`](https://github.com/efureev/go-shutdown): it
stops on `SIGINT`/`SIGTERM` or context cancellation, then runs `Stop` bounded by
`WithShutdownTimeout`, returning `shutdown.ErrShutdownTimeout` if the teardown
does not finish in time. `Stop` honors context cancellation between modules, so
once the timeout fires it aborts promptly instead of leaking in a detached
goroutine; modules not yet stopped are retained for a subsequent `Stop`.

### Communication between modules

The dependency graph only fixes the *order* in which modules start; for
run-time communication the `Manager` shares an `AppContext` (an `EventBus`, a
`Registry` and the logger) and injects it into every module that implements
`ContextAware`. `BaseAppModule` already implements it, so an embedding module
reaches the shared services via `m.AppContext()`. Use `Manager.EventBus()` and
`Manager.Registry()` to reach the same instances from outside.

There are two complementary mechanisms:

**Registry — pull (request/response).** A module *provides* an implementation of
a contract interface; a dependent module *requires* it. Because the contract is
keyed by its Go type, consumers depend on the interface, not on the concrete
module. A `Require[T]` is guaranteed to find its provider as long as the consumer
declares a `Manager` dependency on the providing module (the provider's
`AfterStart` runs first).

```go
type DB interface{ Query(ctx context.Context, key string) (string, error) }

// db module — in its AfterStart:
_ = appmod.Provide[DB](m.AppContext().Registry, m) // m implements DB

// cache module (depends on "db") — in its AfterStart:
db, err := appmod.Require[DB](m.AppContext().Registry)
```

`Provide` rejects a nil implementation with `ErrNilImplementation` — the usual
cause is a constructor that returned `(nil, err)` whose error went unchecked.
Reporting it at `Provide` keeps the mistake where it is made: `Require` itself
never panics, it returns an error.

One asymmetry to know about: `Provide` and `Require` report a nil registry as
`ErrNilRegistry`, but `Revoke` returns a plain `bool` and reports `false` both
for a nil registry and for a contract that was never provided. The two cases are
not distinguishable through its return value; check the registry for nil
yourself if it matters.

**EventBus — push (fire-and-forget).** A module *subscribes* to a value type and
any module *publishes* values of that type. Delivery is synchronous, type-safe,
panic-safe and joins subscriber errors via `errors.Join`.

Events are keyed by their Go type. When the value reaches `Publish` through an
interface variable (an `any`, an `error`, a domain interface), the event is
delivered by its **dynamic** type as well as by the static one, so passing an
event along through a wrapper does not silently drop it. Subscribers registered
for an interface type keep working, and no subscriber is ever called twice.

From inside a module, prefer `SubscribeModule`: it ties the subscription to the
module's lifecycle and removes it on `Destroy`. Plain `Subscribe` hands back an
`Unsubscribe` you must store and call yourself — forget it and a module that is
stopped and started again is subscribed twice, so every event is delivered twice,
growing by one delivery per restart. It is the `EventBus` counterpart of
`Revoke`.

```go
// inside a start hook of a module embedding appmod.BaseAppModule:
err := appmod.SubscribeModule(&m.BaseAppModule, func(ctx context.Context, e UserCreated) error {
    // invalidate, react, ...
    return nil
})
```

For anything else that must be released when the module goes down, register it
next to where it was acquired with `AddCleanup`; cleanups run in reverse
registration order on `Destroy` and on a failed `Init`'s rollback.

```go
type UserCreated struct{ ID string }

// subscriber (e.g. cache, during its start):
unsub, _ := appmod.Subscribe(m.AppContext().Bus, func(_ context.Context, e UserCreated) error {
    // invalidate, react, ...
    return nil
})
defer unsub() // or unsubscribe in BeforeDestroy

// publisher (e.g. api, later):
_ = appmod.Publish(ctx, m.AppContext().Bus, UserCreated{ID: "user:1"})
```

Rule of thumb: use the **Registry** when one module owns data and a caller needs
an answer (`api → cache → db`); use the **EventBus** to broadcast a fact to any
number of interested listeners without expecting a reply.

## Examples

Runnable example applications live under [`examples/`](examples) and cover every
feature of the package:

| Example | Demonstrates |
| --- | --- |
| [`basic`](examples/basic)     | Single-module lifecycle: configuration, the four hooks and the `Created → Running → Destroyed` state machine. |
| [`hooks`](examples/hooks)     | `New(...)` options, `slog` logging, named/prioritized hooks (`AddHook`/`RemoveHook`), the typed `HookError` and automatic rollback. |
| [`manager`](examples/manager) | `Manager` orchestration of a dependency graph, plus inter-module communication: `api → cache → db` data access via `Provide`/`Require` and a `UserCreated` event via the `EventBus`. |

```bash
go run ./examples/basic
go run ./examples/hooks
go run ./examples/manager
```

## Package layout

The package is split into small, focused files:

| File         | Responsibility                                                        |
|--------------|-----------------------------------------------------------------------|
| `appmod.go`  | Package documentation and compile-time contract checks.               |
| `module.go`  | `AppModule` and the narrow `Configurable` / `Named` / `Stateful` / `Lifecycle` / `HookRegistry` interfaces, `HookFunc`, `HookModule`. |
| `config.go`  | `AppModuleConfig`, the `Config` value type and `NewConfig` / `DefaultConfig`. |
| `state.go`   | The lifecycle `State` enum and its `String` method.                   |
| `errors.go`  | Sentinel lifecycle errors.                                            |
| `base.go`    | The embeddable `BaseAppModule` implementation.                        |
| `hook.go`    | The `Phase` and `Hook` types and the typed `HookError`.               |
| `options.go` | Functional options and the `New` constructor.                         |
| `manager.go` | The `Manager` orchestrator: dependency-ordered start/stop, graceful shutdown, health checks. |
| `eventbus.go` | The type-safe `EventBus` for fire-and-forget notifications (`Subscribe`/`Publish`). |
| `registry.go` | The type-safe `Registry` for contract-based access between modules (`Provide`/`Require`/`Revoke`). |
| `appcontext.go` | The shared `AppContext` (`EventBus` + `Registry` + logger + the shutdown broadcast) and the `ContextAware` capability. |

## Development

The repository ships a `Makefile` and `docker-compose.yml` so you don't need a local
Go toolchain.

```bash
make help     # list available commands
make test     # run linters and tests
make gotest   # run tests with race detector and coverage
make lint     # run golangci-lint
make fmt      # format the code
```

Running tests directly:

```bash
go test -race ./...
```

## License

Distributed under the terms of the [MIT License](LICENSE).
