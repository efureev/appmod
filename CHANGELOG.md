# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/).

## [v4.0.0]

Thsi release: removing `EventBus` is breaking.
The module path becomes `github.com/efureev/appmod/v4`. Update imports:

```go
import "github.com/efureev/appmod/v4"
```

### Removed — breaking

- **`EventBus` is gone**, along with `NewEventBus`, `Subscribe`, `SubscribeModule`,
  `Publish`, `Unsubscribe`, `AppContext.Bus`, `Manager.EventBus()` and the errors
  `ErrNilBus`, `ErrNilSubscriber`, `ErrBusClosed` and `ErrNoAppContext`.

  This package orchestrates lifecycles; shipping a second, subtly different
  publish/subscribe implementation alongside the one this project already
  maintains meant a consumer of both got two `Publish` functions with different
  semantics and had to choose. Answering a request and broadcasting a fact are
  different mechanisms with different failure modes — one returns an error to
  the caller, the other queues and may drop — so they are not unified behind an
  interface either: that would make what a call does depend on which
  implementation was wired in.

  **Replacement.** A bus is a capability like any other, published through the
  `Registry`:

  ```go
  bus := msghub.New()
  _ = appmod.Provide[*msghub.Hub](mgr.Registry(), bus)

  // in a module's start hook:
  bus, err := appmod.Require[*msghub.Hub](m.AppContext().Registry)
  ```

  [`msghub`](https://github.com/efureev/msghub) v3 covers everything the
  removed bus did — typed events, synchronous delivery returning the handler's
  error, panic recovery — and adds queued delivery, per-subscriber ordering,
  explicit backpressure and failure reporting.

  `SubscribeModule` moves to the new `adapters/hubmod` module, which keeps its
  behavior: a subscription tied to the module's lifecycle, removed on `Destroy`
  and on a failed `Init`'s rollback.

  One thing does not carry over: the removed bus keyed an event by its dynamic
  type as well as its static one, to survive a value arriving through an `any`
  or an interface. msghub does not need the workaround — the payload type
  comes from an explicitly constructed `Topic[T]`, not from inferring it at the
  call site — so the case cannot arise.

### Added

- **`adapters/` directory** for separate Go modules bridging appmod to other
  libraries. Each has its own `go.mod`, so this module's dependency set is
  unchanged and importing appmod never pulls theirs in.

- **`adapters/hubmod`** (`github.com/efureev/appmod/adapters/hubmod`) ties a
  `msghub` hub to the module lifecycle: `NewModule` owns the hub and drains
  then closes it on teardown, `SubscribeModule` scopes a subscription to a
  module, and `Provide`/`Require` publish the hub through the `Registry`.

- **`adapters/hubmod/examples/orders`**, a runnable application on top of the
  adapter: four modules around one bus, msghub's synchronous and queued delivery
  side by side on the same topic, a module restarted mid-run without being
  subscribed twice, and a teardown that closes the hub after every publisher has
  stopped. Run it with `go run ./examples/orders` from `adapters/hubmod`.

### Changed

- `AppContext` documents the `Registry` as the single extension point through
  which modules share anything, including a bus.
- The `manager` example no longer demonstrates push notifications; the scenario
  moved to `adapters/hubmod`, where it can be shown with a real bus.

## [v3.0.0] 2026-08-06

The module path becomes `github.com/efureev/appmod/v3`. Update imports:

```go
import "github.com/efureev/appmod/v3"
```

### Changed — breaking

- **Migrated to `github.com/efureev/go-shutdown/v3`.** The dependency's redesign
  removes an adapter, fixes a bug appmod was working around by hand, and adds a
  capability the package could not offer before.

- **`Manager.Run` is single use.** The shutdown sequence runs exactly once, so a
  second `Run` returns the new `ErrAlreadyRun` instead of starting the modules
  and returning immediately without ever stopping them. A manager driven through
  `Start`/`Stop` can still be restarted.

- **`Run` now also watches `SIGQUIT`.** It watched `SIGINT` and `SIGTERM`; the
  set is the shutdown package's default and is replaceable with the new
  `WithSignals`.

- **A second signal during teardown terminates the process** with exit code
  128+signum. This is new: v2 had no such behavior. It is the operator's only way
  out of a teardown that hung — the signal handler is installed, so a second
  Ctrl-C would otherwise be delivered to a channel nobody reads. Disable it with
  the new `WithForceOnSecondSignal(false)`, which leaves SIGKILL as the only way
  to stop a module that ignores its context.

### Added

- **Shutdown observation for modules**: `AppContext.Done()` and
  `AppContext.Context()`. A module with a background loop can stop taking new
  work the moment the shutdown starts, instead of waiting for its own `Destroy`
  — which runs only after every module depending on it has already stopped.

  ```go
  go func() {
      for {
          select {
          case <-m.AppContext().Done():
              return // shutdown started
          case job := <-jobs:
              process(job)
          }
      }
  }()
  ```

  It closes for any teardown the manager performs, including the rollback of a
  failed `Start`. An `AppContext` assembled by hand — a public struct, so this
  happens in tests — reports no shutdown rather than panicking. The `*shutdown`
  type stays out of appmod's contract: the field is unexported and reached
  through the two accessors. Demonstrated by the new `worker` module in
  `examples/manager`. Covered by `TestAppContextShutdownObservation`.

- `Manager.Shutdown()` triggers the shutdown a `Run` is waiting on, as a signal
  would. Non-blocking and idempotent.
- `Manager.ExitCode()` reports 128+signum for a signal-triggered shutdown and 0
  otherwise, for `os.Exit`.
- `WithSignals`, `WithForceOnSecondSignal` manager options.
- `ErrShutdownTimeout` is re-exported from the shutdown package, so a caller can
  check for a teardown timeout without importing that package. It wraps
  `context.DeadlineExceeded`.

### Fixed

- **A shutdown timeout no longer hides which module hung.** The dependency can
  only name the hook it was given, and appmod gives it one; `Run` now annotates
  the timeout with the modules that were still running.

  This exposed a real defect in `Manager.Stop`: it cleared `m.started` upfront
  and put the leftovers back only when it reached its next cancellation check —
  so while a module's `Destroy` was blocking, the manager reported that nothing
  was started. `Stop` now removes each module from the set as it finishes, which
  keeps `m.started` truthful at every instant and makes the retention logic
  unnecessary. Covered by `TestManagerRunTimeoutNamesModules`.

- **Removed appmod's workaround for a v2 deficiency.** `Run` detached the
  teardown context from cancellation by hand, because v2 handed the teardown the
  very context whose cancellation had triggered it. v3 detaches unconditionally,
  so the workaround is gone; `TestManagerRun/ContextCancelStopsModules` remains
  as the regression test.

- **Corrected the package documentation**, which still described the rollback as
  running the teardown hooks in reverse order. It has unwound the `AddCleanup`
  compensations since v2.1.0; the doc comment on `Init` was updated then, the
  package-level one was not.

### Removed

- The `slogShutdownLogger` adapter. v3 takes a `*slog.Logger` directly, which
  also removes the one place in the package that stored a `context.Context` in a
  struct field.

## [v2.1.0] 2026-08-06

### Changed — breaking

These tighten the public API before it is used anywhere. Each replaces a
signature or a behavior that could not report a mistake it was in a position to
notice.

- **`Revoke[T](reg)` now returns `(bool, error)`** instead of a bare `bool`, and
  reports `ErrNilRegistry` for a nil registry — the same as `Provide` and
  `Require`. A bare `false` made a wiring mistake indistinguishable from the
  perfectly normal "no such contract".

  ```go
  removed := appmod.Revoke[DB](reg)              // before
  removed, err := appmod.Revoke[DB](reg)         // after
  ```

- **`EventBus.Close()` no longer returns an `error`.** Closing a bus cannot
  fail; an error return that is always nil only invites handling that never
  runs.

- **`BaseAppModule.Initialized()` was removed.** It duplicated
  `State() == StateRunning` exactly, and two ways to ask one question is
  API surface a library has to keep forever. Replace `mod.Initialized()` with
  `mod.State() == appmod.StateRunning`.

- **Rollback of a failed `Init` no longer runs the teardown hooks.** It unwinds
  the cleanups registered with the new `AddCleanup` instead, in reverse order.

  Teardown hooks describe how to stop a module that *finished* starting.
  Running them after a half-finished start meant closing a pool that was never
  opened and a listener that was never bound — the package pairs no start hook
  with a specific teardown hook, so it could not tell which compensations were
  owed and ran all of them. The previous contract papered over this by requiring
  every teardown hook to be nil-safe and idempotent.

  Compensation is now registered by the code that did the work, so a start hook
  that never ran leaves nothing to undo:

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

  **Migration:** a module that releases resources in a `BeforeDestroy` /
  `AfterDestroy` hook keeps working for `Destroy`, but gets no rollback on a
  failed `Init` until that release is registered with `AddCleanup` by the start
  hook that acquired it. `examples/hooks` shows the shape.

### Fixed

- **`Manager.Run` did not stop anything when its context was canceled** — in the
  default configuration. `Run` documents that it blocks until the context is
  canceled or a signal arrives, "after which it gracefully stops every module".
  Without a `WithShutdownTimeout`, the shutdown package passes `Run`'s own
  context to the teardown callback — and the cancellation of that context is
  precisely what woke `Run`. `Stop` saw an already-canceled context, aborted
  before the first module and returned `teardown aborted: context canceled`,
  leaving every module `Running`. With a timeout configured the path happened to
  work, because the shutdown package detaches the context before bounding it.

  `Run` now detaches from cancellation when no timeout is set; with a timeout it
  still honors the bounded context, which is the whole point of the budget.

  `Run` had no test of its own — it is the entry point applications actually
  call, and it was the only exported function of the package with zero coverage.
  Covered now by `TestManagerRun` (context cancel, start failure, shutdown
  timeout).

- **`EventBus` subscriptions outlived their module**: neither `Destroy` nor
  `Manager.Stop` removed them, so a module that was stopped and started again
  was subscribed twice and every published event was delivered twice — one extra
  delivery per restart, while each stale handler kept a live reference to the
  destroyed module. `Subscribe` did return an `Unsubscribe`, but there was
  nowhere natural to keep it, which is the asymmetry that made the leak the
  default: `Registry` has had `Revoke` and a documented "call it in
  BeforeDestroy" since it was introduced.

  The new `SubscribeModule[T](m, fn)` closes it: it subscribes on the bus the
  module received through its `AppContext` and ties the subscription to the
  module's lifecycle, so it is removed on `Destroy` and on a failed `Init`'s
  rollback. Prefer it to plain `Subscribe` from inside a module; `Subscribe`
  remains for callers that own the subscription themselves. The
  `examples/manager` cache module now uses it. Covered by
  `TestSubscribeModuleUnsubscribesOnDestroy`.

- **`Manager.Stop` was serial while `Manager.Start` was concurrent**: `Start`
  brought a whole dependency layer up at once, but `Stop` walked the started
  modules one at a time, so shutdown cost the *sum* of every module's teardown
  instead of the maximum per layer. On an application of mostly independent
  modules that is what overruns a `WithShutdownTimeout` budget and turns a clean
  shutdown into `ErrShutdownTimeout`. `Stop` now reuses the same dependency
  layers, walks them in reverse and tears down each layer concurrently.
  Dependency ordering between layers is unchanged, and the joined error keeps a
  deterministic order rather than depending on scheduling. A graph that has
  become unplannable — possible when a module is registered after `Start` — falls
  back to the previous one-at-a-time reverse teardown. Measured on five
  independent modules with a 40 ms teardown each: from ~200 ms to ~40 ms.
  Covered by `TestManagerStopIsLayered`, `TestManagerStopRespectsLayerOrder` and
  `TestManagerStopFallbackOnUnplannableGraph`.

- **Hooks registered for an unknown `Phase` vanished silently**: `AddHook`,
  `RemoveHook` and the `WithHook` option looked the phase up in an internal
  table and did nothing when it was not one of the four defined values. `Phase`
  is an exported `int32` with no range check, so a typo or a value decoded from
  configuration turned start-up logic into code that never ran, with no error,
  no panic and no log line to notice. All three now panic on an invalid phase.

  **Behavior change:** an out-of-range phase is a programmer error and is
  reported as one. Code that passed an invalid phase was already broken — it
  simply failed quietly; it now fails loudly. The new `Phase.Valid` lets a value
  from an untrusted source be checked before it is passed on. Covered by
  `TestUnknownPhasePanics`, `TestValidPhaseDoesNotPanic` and `TestPhaseValid`.

- **`Config()` returned nil on an unconfigured module**: `BaseAppModule`
  documents its zero value as ready to use, yet `Config()` returned a nil
  interface until `SetConfig` was called — so the package's own idiom,
  `m.Config().Name()` inside a hook (used in `example_test.go` and both
  READMEs), was a nil dereference on exactly that zero value. `Config()` now
  reports `DefaultConfig()` when nothing has been set.

  **Behavior change:** `Name()` follows `Config().Name()` exactly, as its
  documentation always claimed, so an unconfigured module now reports
  `"App Module"` instead of `""`. Because `HookError` omits the module prefix
  only for an empty name, errors from unconfigured modules gain a
  `module "App Module":` prefix — visible in the updated
  `ExampleBaseAppModule_abort` output. Covered by
  `TestZeroValueModuleHasDefaultConfig`.

- **Teardown hooks observed a different state depending on how they were
  reached**: during `Init`'s rollback the module was still `StateInitializing`
  (it moved to `StateFailed` only after the teardown had run), while the same
  hook invoked through `Destroy` saw `StateDestroying`. A hook branching on
  `m.State()` therefore behaved differently on the two paths, and
  `Initializing` is a plainly wrong answer for cleanup code. Rollback now moves
  the module to `StateDestroying` first, so both paths agree. Covered by
  `TestTeardownStateIsConsistent`.

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
    honors context cancellation. `Close` removes all subscriptions.
    `SubscribeModule[T](m, fn)` ties a subscription to a module's lifecycle.
    Sentinel errors `ErrNilBus`, `ErrNilSubscriber`, `ErrBusClosed`,
    `ErrNoAppContext`.
  - **`Registry`** (pull / request-response): a type-safe service locator.
    `Provide[T](reg, impl)` publishes an implementation of contract `T` (usually
    an interface); `Require[T](reg)` obtains it; `Revoke[T](reg)` removes it.
    Sentinel errors `ErrNilRegistry`, `ErrDuplicateProvider`, `ErrProviderNotFound`,
    `ErrNilImplementation`.
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
- **Lifecycle-scoped cleanups**: `BaseAppModule.AddCleanup(CleanupFunc)`
  registers a compensation next to the acquisition that needs it, instead of in
  a separate teardown hook wired up by hand. Cleanups run in reverse
  registration order between the `BeforeDestroy` and `AfterDestroy` phases, and
  during the rollback of a failed `Init`; each runs at most once, so a module
  that is initialized again starts with an empty list. A panicking cleanup is
  recovered and reported as an error, like a hook. `SubscribeModule` is built on
  it. Covered by `TestAddCleanup`.
- EventBus sentinel error `ErrNoAppContext`, returned by `SubscribeModule` when
  the module has not been given an `AppContext` yet.
- `Phase.Valid()` reports whether a `Phase` is one of the four defined lifecycle
  phases, so a value from configuration or a wire format can be checked before
  it reaches `AddHook` / `RemoveHook` / `WithHook`, which panic on an invalid
  one.
- Documented that `Revoke` reports `false` both for a nil registry and for a
  contract that was never provided, and that the two are indistinguishable
  through its return value. This is the one place where the package does not
  report a nil registry as `ErrNilRegistry`: `Revoke` returns a bool, and
  widening it to `(bool, error)` would break every existing caller, so the
  limitation is documented rather than fixed. Pinned by
  `TestRegistryRevokeNilIsIndistinguishable`.

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
- **Automatic rollback** on a failed `Init`: the compensations registered with
  `AddCleanup` are unwound in reverse registration order and their errors are
  joined with the original cause via `errors.Join` (#2). See the breaking-change
  entry above for why teardown hooks are not involved.

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
