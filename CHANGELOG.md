# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- **Events published through an interface were silently dropped**: events are
  keyed by their Go type, but `reflect` reports the *static* type of the
  parameter, so the moment a value reached `Publish` through an interface
  variable — an `any`, an `error`, a domain interface, any wrapper layer — `T`
  was inferred as that interface. `Publish` looked up the interface bucket,
  delivered the event to nobody and returned `nil`: the event vanished without a
  single sign that anything went wrong. Events are now delivered by their
  **dynamic** type as well as by `T`. Subscribers registered for an interface
  type keep working and no subscriber is ever invoked twice, since a subscriber
  belongs to exactly one type key. Covered by
  `TestEventBusPublishThroughInterface`.

- **A nil event panicked inside the subscriber**: `Subscribe` wrapped handlers in
  an unchecked `ev.(T)`, and asserting a nil interface panics regardless of `T`,
  so `Publish`-ing a nil event to a `Subscribe[any]` handler blew up in the
  subscriber (recovered and reported as an error, but still a spurious failure).
  The wrapper now uses a checked assertion and passes the zero value of `T`.
  Found while covering the fix above; same class of defect as the `Require[T]`
  panic below.

- **The `HookModule` view was escapable**: hooks were handed the
  `*BaseAppModule` itself, so a single type assertion recovered `Lifecycle` or
  `HookRegistry` and let a hook re-enter the lifecycle or mutate the hook set
  while it was running — exactly what the narrow view exists to prevent. Hooks
  now receive an opaque view value; asserting it to `Lifecycle`, `HookRegistry`
  or `*BaseAppModule` fails. The documented boundary is now an actual boundary.
  Covered by `TestHookModuleIsOpaque`.

- **Data race in `Manager.Health`** (could crash the process): `Health` copied
  the node *map* by reference under the mutex and then read it after releasing
  the lock, racing a concurrent `Manager.Register`. Concurrent map read/write is
  not a benign race in Go — it aborts the process with
  `fatal error: concurrent map read and map write`, which `recover` cannot catch,
  so a `/healthz` handler running alongside a module registration could take the
  application down. `Health` now collects the `HealthChecker` probes it needs
  under the mutex and runs them (user code, potentially blocking) without holding
  it. Confirmed by the race detector; covered by
  `TestManagerHealthConcurrentRegister`.

- **A second `Manager.Start` tore down the running application**: `Start` had no
  re-entry guard, so a stray or concurrent second call failed on the
  already-initialized modules, treated that as a startup failure and rolled back
  — stopping the modules the *first* `Start` had successfully brought up, leaving
  every module `Destroyed` while the first caller had long since been told
  everything was fine. `Start` is now non-re-entrant and returns the new
  `ErrAlreadyStarted` without touching the running modules; it also refuses to
  run while modules from a previous, context-aborted teardown are still up. After
  `Stop` the manager can be started again. Covered by
  `TestManagerStartIsNotReentrant`.

- **Double teardown on a concurrent `Init`/`Destroy`**: `Init` transitioned the
  module to `StateRunning` *before* running the `AfterStart` hooks, so a
  concurrent `Destroy` passed its state guard and ran the teardown hooks while a
  failing `AfterStart` was rolling the module back through the same hooks. The
  hooks ran twice (double `Close`, double release), `Destroy` reported success,
  and the two final `setState` calls raced. `Init` now publishes `StateRunning`
  only once **both** start phases have succeeded, so a concurrent `Destroy` is
  refused with `ErrNotInitialized`. The race detector cannot find this class of
  bug — every field was correctly mutex-guarded; it was the invariant that was
  broken. Covered by `TestAppModuleAfterStartWindow`.

  **Behavior change:** an `AfterStart` hook now observes `StateInitializing`
  rather than `StateRunning`. This makes the documented guarantee — `Init` either
  fully succeeds (`StateRunning`) or fails (`StateFailed`) — actually hold; it
  previously did not, since the module transiently reported `Running` before
  ending up `Failed`.

- **`Require[T]` panicked on a nil contract**: `Provide[T]` accepted a nil
  implementation silently and `Require[T]` recovered it with an unchecked type
  assertion, panicking with `interface conversion: interface is nil`. The usual
  trigger is a provider whose constructor returned `(nil, err)` with the error
  unchecked. `Provide` now rejects nil interfaces, pointers, maps, slices,
  functions and channels with the new `ErrNilImplementation`, reporting the
  mistake where it is made, and `Require` uses a checked assertion so it can no
  longer panic under any circumstance. Covered by `TestRegistryProvideNil`.

- **Hook ordering inverted at extreme priorities**: `orderHooks` compared hooks
  with `a.Priority - b.Priority`, which overflows `int` for operands as far apart
  as `math.MinInt` and `math.MaxInt` and silently reverses the order. It now uses
  `cmp.Compare`. Covered by `TestHookPriorityExtremes`.

- **Goroutine leak on shutdown timeout**: `Manager.Stop` now honors context
  cancellation between modules. When `Manager.Run` hits its `WithShutdownTimeout`
  and the shutdown package cancels the teardown context, `Stop` aborts before the
  next module (returning a `context.Canceled`/`DeadlineExceeded`-wrapping error)
  instead of running to completion in a detached goroutine. Modules that were not
  torn down are retained so a subsequent `Stop` can finish the teardown.

### Added

- **Inter-module communication** through two complementary mechanisms shared via
  a new `AppContext` (`Bus` + `Registry` + `Logger`) that the `Manager` injects
  into every `ContextAware` module before start (`BaseAppModule` implements
  `ContextAware`; reach the context via `m.AppContext()`):
  - **`EventBus`** (push / fire-and-forget): a type-safe publish/subscribe bus.
    `Subscribe[T](bus, fn)` registers a handler for events of type `T` and
    returns an idempotent `Unsubscribe`; `Publish[T](ctx, bus, ev)` delivers
    synchronously, joins subscriber errors via `errors.Join`, recovers panics and
    honors context cancellation. `Close` removes all subscriptions. Sentinel
    errors `ErrNilBus`, `ErrNilSubscriber`, `ErrBusClosed`.
  - **`Registry`** (pull / request-response): a type-safe service locator.
    `Provide[T](reg, impl)` publishes an implementation of contract `T` (usually
    an interface); `Require[T](reg)` obtains it; `Revoke[T](reg)` removes it.
    Sentinel errors `ErrNilRegistry`, `ErrDuplicateProvider`, `ErrProviderNotFound`.
  - `Manager` exposes the shared services via `Manager.EventBus()` /
    `Manager.Registry()`. The `examples/manager` example was reworked to show
    `api → cache → db` data access through `Require`/`Provide` and a `UserCreated`
    event delivered through the bus.

- **Configurable graceful shutdown** in `Manager.Run`: the signal handling and
  the timeout-bounded teardown are now delegated to
  `github.com/efureev/go-shutdown/v2` (`OnDestroy → Manager.Stop`). `Run` still
  reacts to `SIGINT`/`SIGTERM` and to context cancellation, and now returns
  `shutdown.ErrShutdownTimeout` when the teardown exceeds the configured
  `WithShutdownTimeout`. The package's structured `*slog.Logger` is bridged to
  the shutdown logger via an internal adapter.

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
  `ErrDuplicateModule`, `ErrUnknownDependency`, `ErrDependencyCycle`,
  `ErrAlreadyStarted`.
- Registry sentinel error `ErrNilImplementation`, returned by `Provide` for a nil
  implementation.

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
  reverse priority order (the reverse of the order they run in during `Destroy`)
  and rollback errors are joined with the original cause via `errors.Join` (#2).
  Rollback runs **every** teardown hook, not only those paired with a start hook
  that actually completed — the package does not track how far the start phase
  got. Teardown hooks must therefore be nil-safe and idempotent: a `BeforeStart`
  hook failing first still triggers the full teardown.

### Changed

- **Breaking:** `HookFunc` now receives the narrow read-only `HookModule` view
  (`Configurable` + `Named` + `Stateful`) instead of the full `AppModule`, so a
  hook can no longer re-enter `Init` / `Destroy` or mutate the hook set (#3). The
  value handed to a hook is an opaque view rather than the module itself, so the
  narrowing cannot be undone with a type assertion (see the corresponding entry
  under **Fixed**).
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
