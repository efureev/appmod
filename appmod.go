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
// (on Destroy). Any hook may abort the lifecycle by returning an error.
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
// context is canceled, the already-executed work is rolled back by running the
// teardown hooks in reverse order and the module ends up in [StateFailed]. The
// lifecycle is context-aware — the context is checked between hooks, so a
// canceled context aborts the remaining start/stop hooks.
//
// [BaseAppModule] is safe for concurrent use: lifecycle transitions, hook
// registration and configuration access are guarded by a mutex. A panic in a
// hook is recovered and converted into an error instead of crashing the
// application.
//
// The package is organized into focused files:
//
//	module.go  — the AppModule contract and the narrow Configurable / Lifecycle /
//	             HookRegistry interfaces plus the HookFunc type.
//	config.go  — the AppModuleConfig interface, the Config value type and its
//	             constructors (NewConfig, DefaultConfig).
//	state.go   — the lifecycle State enum and its String method.
//	errors.go  — the sentinel lifecycle errors.
//	base.go    — the embeddable BaseAppModule implementation.
//	options.go — the functional options and the New constructor.
//	manager.go — the Manager orchestrator: dependency-ordered start/stop of
//	             multiple modules, graceful shutdown and health checks.
//
// For applications composed of several inter-dependent modules, [Manager]
// orchestrates them: modules are registered with their dependencies and started
// in topological order (independent modules concurrently) and stopped in the
// reverse order.
package appmod

// Compile-time checks that the contracts are satisfied.
var (
	_ AppModule       = (*BaseAppModule)(nil)
	_ AppModuleConfig = Config{}
)
