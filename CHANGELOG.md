# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

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
