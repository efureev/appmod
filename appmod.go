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
// The lifecycle is guarded by an internal state flag: calling Init twice or
// calling Destroy on a module that was not initialized returns a sentinel error
// ([ErrAlreadyInitialized] / [ErrNotInitialized]).
package appmod

import (
	"context"
	"errors"
	"fmt"
)

// Compile-time checks that the contracts are satisfied.
var (
	_ AppModule       = (*BaseAppModule)(nil)
	_ AppModuleConfig = Config{}
)

// Lifecycle errors returned by [BaseAppModule].
var (
	// ErrAlreadyInitialized is returned by Init when the module has already
	// been initialized and not yet destroyed.
	ErrAlreadyInitialized = errors.New("appmod: module already initialized")
	// ErrNotInitialized is returned by Destroy when the module has not been
	// initialized (or has already been destroyed).
	ErrNotInitialized = errors.New("appmod: module is not initialized")
)

// HookFunc is a lifecycle hook. It receives the lifecycle context and the
// module the hook is attached to. Returning a non-nil error aborts the
// corresponding lifecycle phase.
type HookFunc func(ctx context.Context, mod AppModule) error

// AppModuleConfig describes module configuration.
type AppModuleConfig interface {
	// Name returns the app module name.
	Name() string
	// Version returns the app module version.
	Version() string
}

// AppModule describes a module lifecycle.
type AppModule interface {
	// SetConfig sets the module configuration.
	SetConfig(config AppModuleConfig)
	// Config returns the module configuration.
	Config() AppModuleConfig

	// Init runs BeforeStart hooks, marks the module as initialized and runs
	// AfterStart hooks.
	Init(ctx context.Context) error
	// Destroy runs BeforeDestroy hooks, marks the module as not initialized
	// and runs AfterDestroy hooks.
	Destroy(ctx context.Context) error

	// BeforeStart registers a hook executed before the module is started.
	BeforeStart(fn HookFunc)
	// AfterStart registers a hook executed after the module is started.
	AfterStart(fn HookFunc)
	// BeforeDestroy registers a hook executed before the module is destroyed.
	BeforeDestroy(fn HookFunc)
	// AfterDestroy registers a hook executed after the module is destroyed.
	AfterDestroy(fn HookFunc)
}

// Config is a basic immutable [AppModuleConfig] implementation.
type Config struct {
	name    string
	version string
}

// Name returns the app module name.
func (c Config) Name() string {
	return c.name
}

// Version returns the app module version.
func (c Config) Version() string {
	return c.version
}

// BaseAppModule is an abstract, embeddable base implementation of [AppModule].
type BaseAppModule struct {
	config AppModuleConfig

	beforeStartFns   []HookFunc
	afterStartFns    []HookFunc
	beforeDestroyFns []HookFunc
	afterDestroyFns  []HookFunc

	initialized bool
}

// Config returns the app module config.
func (b *BaseAppModule) Config() AppModuleConfig {
	return b.config
}

// SetConfig sets the config of the app module.
func (b *BaseAppModule) SetConfig(config AppModuleConfig) {
	b.config = config
}

// Initialized reports whether the module is currently initialized.
func (b *BaseAppModule) Initialized() bool {
	return b.initialized
}

// Init initializes the app module.
//
// It runs all BeforeStart hooks, marks the module as initialized and runs all
// AfterStart hooks. If the module is already initialized it returns
// [ErrAlreadyInitialized]. If any BeforeStart hook fails, the error is wrapped
// and the module stays not initialized.
func (b *BaseAppModule) Init(ctx context.Context) error {
	if b.initialized {
		return ErrAlreadyInitialized
	}

	if err := b.runHooks(ctx, b.beforeStartFns); err != nil {
		return fmt.Errorf("appmod: BeforeStart hook failed: %w", err)
	}

	b.initialized = true

	if err := b.runHooks(ctx, b.afterStartFns); err != nil {
		return fmt.Errorf("appmod: AfterStart hook failed: %w", err)
	}

	return nil
}

// Destroy tears down the app module.
//
// It runs all BeforeDestroy hooks, marks the module as not initialized and runs
// all AfterDestroy hooks. If the module is not initialized it returns
// [ErrNotInitialized]. If any BeforeDestroy hook fails, the error is wrapped and
// the module stays initialized.
func (b *BaseAppModule) Destroy(ctx context.Context) error {
	if !b.initialized {
		return ErrNotInitialized
	}

	if err := b.runHooks(ctx, b.beforeDestroyFns); err != nil {
		return fmt.Errorf("appmod: BeforeDestroy hook failed: %w", err)
	}

	b.initialized = false

	if err := b.runHooks(ctx, b.afterDestroyFns); err != nil {
		return fmt.Errorf("appmod: AfterDestroy hook failed: %w", err)
	}

	return nil
}

// runHooks executes the given hooks in registration order, stopping at the
// first error.
func (b *BaseAppModule) runHooks(ctx context.Context, hooks []HookFunc) error {
	for _, fn := range hooks {
		if fn == nil {
			continue
		}
		if err := fn(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

// BeforeStart registers a hook executed before the module is started.
func (b *BaseAppModule) BeforeStart(fn HookFunc) {
	b.beforeStartFns = append(b.beforeStartFns, fn)
}

// AfterStart registers a hook executed after the module is started.
func (b *BaseAppModule) AfterStart(fn HookFunc) {
	b.afterStartFns = append(b.afterStartFns, fn)
}

// BeforeDestroy registers a hook executed before the module is destroyed.
func (b *BaseAppModule) BeforeDestroy(fn HookFunc) {
	b.beforeDestroyFns = append(b.beforeDestroyFns, fn)
}

// AfterDestroy registers a hook executed after the module is destroyed.
func (b *BaseAppModule) AfterDestroy(fn HookFunc) {
	b.afterDestroyFns = append(b.afterDestroyFns, fn)
}

// DefaultConfig returns a default config (`App Module`, `v0.0.1`).
func DefaultConfig() AppModuleConfig {
	return Config{`App Module`, `v0.0.1`}
}

// NewConfig creates a new basic config.
func NewConfig(name, version string) AppModuleConfig {
	return Config{name, version}
}
