package appmod

import "context"

// HookFunc is a lifecycle hook. It receives the lifecycle context and a narrow
// [HookModule] view of the module the hook is attached to. Returning a non-nil
// error aborts the corresponding lifecycle phase.
//
// The view is intentionally narrow: it exposes configuration, name and state
// but not [Lifecycle] or [HookRegistry], so a hook cannot re-enter Init/Destroy
// or mutate the hook set while it is running.
type HookFunc func(ctx context.Context, mod HookModule) error

// Named exposes the module name.
type Named interface {
	// Name returns the module name (a shortcut for Config().Name()).
	Name() string
}

// Stateful exposes the current lifecycle state of a module.
type Stateful interface {
	// State returns the current lifecycle state.
	State() State
}

// HookModule is the narrow view of a module passed to a [HookFunc]. It grants
// access to the configuration, name and state but deliberately omits the
// [Lifecycle] and [HookRegistry] capabilities to discourage re-entrant or
// shared-state mutations from within a hook.
type HookModule interface {
	Configurable
	Named
	Stateful
}

// Configurable describes the configuration contract of a module.
type Configurable interface {
	// SetConfig sets the module configuration.
	SetConfig(config AppModuleConfig)
	// Config returns the module configuration.
	Config() AppModuleConfig
}

// Lifecycle describes the start/stop contract of a module.
type Lifecycle interface {
	// Init runs BeforeStart hooks, marks the module as initialized and runs
	// AfterStart hooks.
	Init(ctx context.Context) error
	// Destroy runs BeforeDestroy hooks, marks the module as not initialized
	// and runs AfterDestroy hooks.
	Destroy(ctx context.Context) error
}

// HookRegistry describes the lifecycle-hook registration contract of a module.
type HookRegistry interface {
	// BeforeStart registers an anonymous hook executed before the module is started.
	BeforeStart(fn HookFunc)
	// AfterStart registers an anonymous hook executed after the module is started.
	AfterStart(fn HookFunc)
	// BeforeDestroy registers an anonymous hook executed before the module is destroyed.
	BeforeDestroy(fn HookFunc)
	// AfterDestroy registers an anonymous hook executed after the module is destroyed.
	AfterDestroy(fn HookFunc)
	// AddHook registers a named, prioritized hook for the given phase.
	AddHook(phase Phase, hook Hook)
	// RemoveHook removes a previously registered named hook from the given phase.
	// It reports whether a hook was removed.
	RemoveHook(phase Phase, name string) bool
}

// AppModule describes a full module: configuration, lifecycle and hooks.
//
// It is composed of the narrower [Configurable], [Named], [Stateful],
// [Lifecycle] and [HookRegistry] interfaces so that consumers can depend only
// on the capability they need.
type AppModule interface {
	Configurable
	Named
	Stateful
	Lifecycle
	HookRegistry
}
