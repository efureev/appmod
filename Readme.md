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
- Hooks receive a narrow, read-only `HookModule` view (config/name/state) instead of the full module.
- Hooks can abort startup/shutdown by returning an `error`, reported as a typed `HookError` (phase, index, name, module).
- Optional **per-module structured logging** (`slog`) of lifecycle transitions and phase durations.
- Idempotency guard: double `Init` or `Destroy` before `Init` returns a sentinel error.
- Explicit lifecycle **state machine** (`Created → Initializing → Running → Destroying → Destroyed`, plus `Failed`) exposed via `State()`.
- **Context-aware** lifecycle: hooks are skipped once the context is canceled.
- **Atomic `Init`**: any start-hook failure (or context cancellation) triggers an automatic rollback (teardown hooks run in reverse order) and leaves the module in `StateFailed`.
- Embeddable `BaseAppModule` — implement your own module by embedding it.
- **Concurrency-safe**: lifecycle, hook registration and config access are mutex-guarded.
- **Panic-safe hooks**: a panic in a hook is recovered and returned as an error.
- Narrow capability interfaces (`Configurable` / `Named` / `Stateful` / `Lifecycle` / `HookRegistry`) composed into `AppModule`.
- `New(opts ...Option)` constructor with functional options.
- **Module orchestrator** `Manager`: dependency-ordered (topological) start with concurrent start of independent modules, reverse-order stop, dependency-cycle detection, `SIGINT`/`SIGTERM`-aware graceful shutdown and optional health checks.

## Requirements

- Go **1.24** or newer.

## Install

```bash
go get github.com/efureev/appmod/v2
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

`Init` is **atomic**: if any start hook (`BeforeStart` or `AfterStart`) returns
an error, or the context is canceled, the module automatically rolls back by
running the teardown hooks (`BeforeDestroy`, then `AfterDestroy`) in reverse
registration order and ends up in `StateFailed`. Rollback errors are joined with
the original cause via `errors.Join`. The module is therefore never left
half-started: `Init` either fully succeeds (`StateRunning`) or fails
(`StateFailed`).

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

	"github.com/efureev/appmod/v2"
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
