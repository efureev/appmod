// Package appmod provides a tiny, dependency-free building block for structuring
// an application as a set of modules with a common lifecycle.
//
// A module is described by the [AppModule] interface and the most common
// implementation is provided by the embeddable [BaseAppModule]. Each module
// has a configuration ([AppModuleConfig]) and a lifecycle managed by
// [BaseAppModule.Init] and [BaseAppModule.Destroy].
//
// Around the lifecycle a module exposes four sets of hooks that are executed in
// order: BeforeStart, AfterStart (on Init) and BeforeDestroy, AfterDestroy
// (on Destroy). Hooks can be anonymous or named and prioritized (see [Hook] and
// [BaseAppModule.AddHook] / [BaseAppModule.RemoveHook]); within a phase they run
// in ascending priority order. Any hook may abort the lifecycle by returning an
// error, which is reported as a [HookError]. A hook receives the narrow,
// read-only [HookModule] view rather than the full [AppModule].
//
// The lifecycle is modeled as an explicit state machine (see [State]):
//
//	Created → Initializing → Running → Destroying → Destroyed
//
// Calling Init while the module is already running (or being initialized) and
// calling Destroy on a module that is not running returns a sentinel error
// ([ErrAlreadyInitialized] / [ErrNotInitialized]).
//
// Init is atomic with respect to failures: if any start hook fails or the
// context is canceled, the work that did happen is rolled back by unwinding the
// compensations registered with [BaseAppModule.AddCleanup], in reverse order,
// and the module ends up in [StateFailed]. The lifecycle is context-aware — the
// context is checked between hooks, so a canceled context aborts the remaining
// start/stop hooks.
//
// [BaseAppModule] is safe for concurrent use: lifecycle transitions, hook
// registration and configuration access are guarded by a mutex. A panic in a
// hook is recovered and converted into an error instead of crashing the
// application.
//
// The package is organized into focused files:
//
//	appmod.go     — the package documentation and the compile-time contract
//	                checks.
//	module.go     — the AppModule contract and the narrow Configurable / Named /
//	                Stateful / Lifecycle / HookRegistry interfaces, HookFunc and
//	                the read-only HookModule view.
//	config.go     — the AppModuleConfig interface, the Config value type and its
//	                constructors (NewConfig, DefaultConfig).
//	hook.go       — the Phase and Hook types and the typed HookError.
//	state.go      — the lifecycle State enum and its String method.
//	errors.go     — the sentinel lifecycle errors.
//	base.go       — the embeddable BaseAppModule implementation.
//	options.go    — the functional options and the New constructor.
//	manager.go    — the Manager orchestrator: dependency-ordered start/stop of
//	                multiple modules, graceful shutdown and health checks.
//	registry.go   — the type-safe Registry for contract-based access between
//	                modules (Provide / Require / Revoke).
//	appcontext.go — the shared AppContext (Registry + Logger + the shutdown
//	                broadcast) and the ContextAware capability used by the
//	                Manager to inject it.
//
// The adapters/ directory holds separate Go modules that bridge appmod to other
// libraries. They are not part of this module, so importing appmod never pulls
// their dependencies in.
//
// For applications composed of several inter-dependent modules, [Manager]
// orchestrates them: modules are registered with their dependencies and started
// in topological order, then stopped in the reverse one. Both directions run the
// modules of a dependency layer concurrently, so start-up and shutdown each cost
// the slowest module per layer rather than the sum over all of them.
//
// Modules reach one another at run time through the [Registry] shared via the
// [AppContext] that the Manager injects into every [ContextAware] module: a
// provider publishes a contract with [Provide], a consumer takes it with
// [Require], and [Revoke] releases it so a restarted module does not leave a
// stale implementation behind.
//
// The Registry is the single extension point, and that includes messaging: this
// package deliberately ships no event bus. Publishing notifications and
// answering requests are different mechanisms with different failure modes —
// one queues and may drop, the other returns an error — and hiding that
// difference behind one interface makes the behavior of a call depend on which
// implementation was wired in. An application that wants a bus provides one
// through the Registry like any other contract; github.com/efureev/msghub
// is the one this project maintains, and adapters/hubmod ties its lifecycle to
// a module's.
//
// The AppContext also carries the shutdown broadcast: [AppContext.Done] closes
// as soon as the application starts going down, so a module with a background
// loop can stop taking new work immediately instead of waiting for its own
// Destroy — which runs only after every module depending on it has stopped.
package appmod

// Compile-time checks that the contracts are satisfied.
var (
	_ AppModule       = (*BaseAppModule)(nil)
	_ AppModuleConfig = Config{}
	_ ContextAware    = (*BaseAppModule)(nil)
	_ HookModule      = hookView{}
)
