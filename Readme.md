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
- Hooks can abort startup/shutdown by returning an `error`.
- Idempotency guard: double `Init` or `Destroy` before `Init` returns a sentinel error.
- Embeddable `BaseAppModule` — implement your own module by embedding it.

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

// HookFunc is a lifecycle hook.
type HookFunc func(ctx context.Context, mod AppModule) error

// AppModule describes a module lifecycle.
type AppModule interface {
    SetConfig(config AppModuleConfig)
    Config() AppModuleConfig

    Init(ctx context.Context) error
    Destroy(ctx context.Context) error

    BeforeStart(fn HookFunc)
    AfterStart(fn HookFunc)
    BeforeDestroy(fn HookFunc)
    AfterDestroy(fn HookFunc)
}
```

The lifecycle is guarded by an internal state flag. Calling `Init` twice returns
`ErrAlreadyInitialized`; calling `Destroy` on a module that was not initialized
returns `ErrNotInitialized`.

### Constructors

| Function                       | Description                                   |
|--------------------------------|-----------------------------------------------|
| `NewConfig(name, version)`     | Creates a config with the given name/version. |
| `DefaultConfig()`              | Returns a default config (`App Module`, `v0.0.1`). |

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
