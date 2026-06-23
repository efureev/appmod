# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **Runnable examples** under `examples/` (`basic`, `hooks`, `manager`) covering
  the single-module lifecycle, the advanced hook features (named/prioritized/
  removable hooks, `slog` logging, `HookError`, rollback) and the `Manager`
  orchestrator (dependency graph, concurrent start, `Health`, graceful `Run`).

- **Named, prioritized and removable hooks** (#3): the new `Hook` struct
  (`Name`, `Priority`, `Run`) and `Phase` enum, plus `AddHook(phase, hook)` /
  `RemoveHook(phase, name)` on `BaseAppModule` (and in the `HookRegistry`
  interface) and the `WithHook` option. Hooks within a phase now run in
  ascending priority order (stable for equal priorities) (#3).
- **Per-module structured logging** (#4): an optional `*slog.Logger` on
  `BaseAppModule` (via `SetLogger` or the `WithModuleLogger` option) that reports
  lifecycle transitions and phase durations; it defaults to a no-op logger.
- **Typed hook error** `HookError{Phase, Index, Name, Module, Err}` (#4) returned
  by `Init` / `Destroy` (and rollback) so a failing hook can be identified
  programmatically via `errors.As`; it unwraps to the original cause.
- Narrow `Named` and `Stateful` interfaces and the read-only `HookModule` view
  now passed to hooks instead of the full `AppModule` (#3).

- **Module orchestrator** `Manager` (variant C): register named modules with
  their dependencies and start them in dependency (topological) order, starting
  independent modules concurrently, and stop them in reverse order. Includes
  dependency-cycle and unknown-dependency detection, error aggregation via
  `errors.Join`, `slog`-based logging, `SIGINT`/`SIGTERM`-aware graceful
  shutdown (`Run`), an optional `HealthChecker` interface with `Health`, and the
  `NewManager` constructor with `WithLogger` / `WithShutdownTimeout` options.
- Orchestration sentinel errors `ErrEmptyName`, `ErrNilModule`,
  `ErrDuplicateModule`, `ErrUnknownDependency`, `ErrDependencyCycle`.

- `BaseAppModule` is now **safe for concurrent use**: lifecycle transitions, hook
  registration and configuration access are guarded by an internal mutex (#1).
- Hooks are now **panic-safe**: a panic raised inside a hook is recovered and
  returned as an error instead of crashing the application (#1).
- Narrow capability interfaces `Configurable`, `Lifecycle` and `HookRegistry`;
  `AppModule` is now composed of them so consumers can depend only on what they
  need (#3).
- `New(opts ...Option)` constructor with functional options
  `WithConfig`, `WithBeforeStart`, `WithAfterStart`, `WithBeforeDestroy`,
  `WithAfterDestroy` (#3).
- Explicit, public lifecycle **state machine** `State`
  (`Created → Initializing → Running → Destroying → Destroyed`, plus `Failed`)
  with a `State()` accessor and a `String()` method (#2).
- **Context-aware** lifecycle: `Init` / `Destroy` check the context between hooks
  and abort the remaining hooks when the context is canceled (#2).
- **Automatic rollback** on a failed `Init`: the teardown hooks are run in
  reverse registration order to release resources acquired by start hooks that
  already ran; rollback errors are joined with the original cause via
  `errors.Join` (#2).

### Changed

- **Breaking:** `HookFunc` now receives the narrow read-only `HookModule` view
  (`Configurable` + `Named` + `Stateful`) instead of the full `AppModule`, so a
  hook can no longer re-enter `Init` / `Destroy` or mutate the hook set (#3).
- **Breaking:** failing hooks are now reported as `*HookError` (with the phase,
  index, hook name and module name) instead of a plain `fmt.Errorf` string (#4).
- Internal hook storage is now `[]Hook` (named + prioritized); `AddHook` /
  `RemoveHook` were added to the `HookRegistry` interface (#3).
- `BaseAppModule` now also implements `Named` (`Name()`) and exposes
  `SetLogger`; `AppModule` is composed of `Named` and `Stateful` too (#3, #4).
- Concurrent / repeated `Init` and `Destroy` are now handled through an internal
  lifecycle state instead of a plain boolean flag (#1).
- `Init` failure semantics are now **atomic**: any start-hook failure (including
  `AfterStart`) or context cancellation rolls the module back and leaves it in
  `StateFailed`; the module is never left half-started (#2).
- `NewConfig` and `DefaultConfig` now return the concrete `Config` type instead
  of the `AppModuleConfig` interface (#3).
- Documented the immutability contract of `Config` relative to `SetConfig` (#3).
- Renamed the misleading `Events/PanicMode` test to `Events/HookError` and added
  `Events/HookPanic`, `New/Options` and a concurrent `Init`/`Destroy` test (#5).
- Split the single `appmod.go` into focused files (`module.go`, `config.go`,
  `state.go`, `errors.go`, `base.go`, `options.go`, `hook.go`); `appmod.go` now
  only holds the package documentation and the compile-time contract checks (#5).
