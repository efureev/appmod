package appmod

import "context"

// HookFunc is a lifecycle hook. It receives the lifecycle context and the
// module the hook is attached to. Returning a non-nil error aborts the
// corresponding lifecycle phase.
type HookFunc func(ctx context.Context, mod AppModule) error

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
	// BeforeStart registers a hook executed before the module is started.
	BeforeStart(fn HookFunc)
	// AfterStart registers a hook executed after the module is started.
	AfterStart(fn HookFunc)
	// BeforeDestroy registers a hook executed before the module is destroyed.
	BeforeDestroy(fn HookFunc)
	// AfterDestroy registers a hook executed after the module is destroyed.
	AfterDestroy(fn HookFunc)
}

// AppModule describes a full module: configuration, lifecycle and hooks.
//
// It is composed of the narrower [Configurable], [Lifecycle] and [HookRegistry]
// interfaces so that consumers can depend only on the capability they need.
type AppModule interface {
	Configurable
	Lifecycle
	HookRegistry
}
