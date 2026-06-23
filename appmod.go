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
package appmod

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
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

// Config is a basic immutable [AppModuleConfig] implementation.
//
// A Config value never changes after construction; its fields are unexported
// and can only be set through [NewConfig] or [DefaultConfig]. Reconfiguring a
// module is therefore done by swapping the whole value via
// [BaseAppModule.SetConfig] rather than by mutating a Config in place.
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

// State is the lifecycle state of a [BaseAppModule].
//
// The normal flow is:
//
//	Created → Initializing → Running → Destroying → Destroyed
//
// If a hook aborts Init (or the context is canceled during start), the module
// ends up in StateFailed after the automatic rollback (see [BaseAppModule.Init]).
type State int32

const (
	// StateCreated is the initial state of a freshly created module.
	StateCreated State = iota
	// StateInitializing means Init is currently running start hooks.
	StateInitializing
	// StateRunning means the module has been successfully initialized.
	StateRunning
	// StateDestroying means Destroy is currently running teardown hooks.
	StateDestroying
	// StateDestroyed means the module has been successfully destroyed.
	StateDestroyed
	// StateFailed means Init failed; any acquired resources were rolled back.
	StateFailed
)

// String implements [fmt.Stringer].
func (s State) String() string {
	switch s {
	case StateCreated:
		return "Created"
	case StateInitializing:
		return "Initializing"
	case StateRunning:
		return "Running"
	case StateDestroying:
		return "Destroying"
	case StateDestroyed:
		return "Destroyed"
	case StateFailed:
		return "Failed"
	default:
		return fmt.Sprintf("State(%d)", int32(s))
	}
}

// BaseAppModule is an abstract, embeddable base implementation of [AppModule].
//
// The zero value is ready to use and safe for concurrent use by multiple
// goroutines.
type BaseAppModule struct {
	mu sync.Mutex

	config AppModuleConfig

	beforeStartFns   []HookFunc
	afterStartFns    []HookFunc
	beforeDestroyFns []HookFunc
	afterDestroyFns  []HookFunc

	state State
}

// State returns the current lifecycle state of the module.
func (b *BaseAppModule) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state
}

// Config returns the app module config.
func (b *BaseAppModule) Config() AppModuleConfig {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.config
}

// SetConfig sets the config of the app module.
func (b *BaseAppModule) SetConfig(config AppModuleConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.config = config
}

// Initialized reports whether the module is currently running (its Init
// completed successfully and Destroy has not run yet).
func (b *BaseAppModule) Initialized() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state == StateRunning
}

// Init initializes the app module.
//
// It runs all BeforeStart hooks, transitions the module to [StateRunning] and
// then runs all AfterStart hooks. Init can only be started from [StateCreated],
// [StateDestroyed] or [StateFailed]; otherwise it returns
// [ErrAlreadyInitialized].
//
// Init is context-aware: the context is checked before every start hook and a
// canceled context aborts the remaining hooks.
//
// Failure semantics are atomic: if any start hook (BeforeStart or AfterStart)
// fails or the context is canceled, Init automatically rolls back by running
// the teardown hooks (see [BaseAppModule.rollback]) and leaves the module in
// [StateFailed]. The module is therefore never left half-started: Init either
// fully succeeds ([StateRunning]) or fails ([StateFailed]). Any rollback error
// is joined with the original cause via [errors.Join].
func (b *BaseAppModule) Init(ctx context.Context) error {
	b.mu.Lock()
	switch b.state {
	case StateCreated, StateDestroyed, StateFailed:
		// A fresh, destroyed or previously failed module can be (re)initialized.
	default:
		b.mu.Unlock()
		return ErrAlreadyInitialized
	}
	b.state = StateInitializing
	beforeStart := slices.Clone(b.beforeStartFns)
	b.mu.Unlock()

	if err := b.runHooks(ctx, beforeStart); err != nil {
		return b.failInit(ctx, fmt.Errorf("appmod: BeforeStart hook failed: %w", err))
	}

	b.mu.Lock()
	b.state = StateRunning
	afterStart := slices.Clone(b.afterStartFns)
	b.mu.Unlock()

	if err := b.runHooks(ctx, afterStart); err != nil {
		return b.failInit(ctx, fmt.Errorf("appmod: AfterStart hook failed: %w", err))
	}

	return nil
}

// failInit performs the rollback for a failed Init, marks the module as
// [StateFailed] and returns the (possibly joined) error.
func (b *BaseAppModule) failInit(ctx context.Context, cause error) error {
	if rbErr := b.rollback(ctx); rbErr != nil {
		cause = errors.Join(cause, fmt.Errorf("appmod: rollback failed: %w", rbErr))
	}
	b.setState(StateFailed)

	return cause
}

// rollback compensates a failed Init by running the teardown hooks
// (BeforeDestroy then AfterDestroy) in reverse registration order, so that
// resources acquired by the start hooks that already ran are released.
//
// Unlike the start phases, rollback does not abort on context cancellation: it
// always attempts every teardown hook and joins all resulting errors so that no
// cleanup is skipped.
func (b *BaseAppModule) rollback(ctx context.Context) error {
	b.mu.Lock()
	beforeDestroy := slices.Clone(b.beforeDestroyFns)
	afterDestroy := slices.Clone(b.afterDestroyFns)
	b.mu.Unlock()

	var errs []error
	errs = append(errs, b.runTeardown(ctx, beforeDestroy)...)
	errs = append(errs, b.runTeardown(ctx, afterDestroy)...)

	return errors.Join(errs...)
}

// Destroy tears down the app module.
//
// It runs all BeforeDestroy hooks, transitions the module to [StateDestroyed]
// and runs all AfterDestroy hooks. Destroy can only be called on a running
// module ([StateRunning]); otherwise it returns [ErrNotInitialized].
//
// Destroy is context-aware: the context is checked before every teardown hook.
// If a BeforeDestroy hook fails (or the context is canceled before the module
// is marked destroyed), the error is wrapped and the module stays in
// [StateRunning] so that Destroy can be retried.
func (b *BaseAppModule) Destroy(ctx context.Context) error {
	b.mu.Lock()
	if b.state != StateRunning {
		b.mu.Unlock()
		return ErrNotInitialized
	}
	b.state = StateDestroying
	beforeDestroy := slices.Clone(b.beforeDestroyFns)
	b.mu.Unlock()

	if err := b.runHooks(ctx, beforeDestroy); err != nil {
		b.setState(StateRunning)
		return fmt.Errorf("appmod: BeforeDestroy hook failed: %w", err)
	}

	b.mu.Lock()
	b.state = StateDestroyed
	afterDestroy := slices.Clone(b.afterDestroyFns)
	b.mu.Unlock()

	if err := b.runHooks(ctx, afterDestroy); err != nil {
		return fmt.Errorf("appmod: AfterDestroy hook failed: %w", err)
	}

	return nil
}

// setState atomically updates the internal lifecycle state.
func (b *BaseAppModule) setState(s State) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = s
}

// runHooks executes the given hooks in registration order, stopping at the
// first error. A panic raised by a hook is recovered and returned as an error.
//
// The context is checked before each hook so that a canceled or timed-out
// context aborts the remaining hooks with the context error.
func (b *BaseAppModule) runHooks(ctx context.Context, hooks []HookFunc) error {
	for _, fn := range hooks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if fn == nil {
			continue
		}
		if err := b.runHook(ctx, fn); err != nil {
			return err
		}
	}
	return nil
}

// runTeardown executes the given teardown hooks in reverse registration order,
// collecting every error instead of stopping at the first one. It is used by
// [BaseAppModule.rollback] and intentionally ignores context cancellation so
// that cleanup always runs to completion.
func (b *BaseAppModule) runTeardown(ctx context.Context, hooks []HookFunc) []error {
	var errs []error
	for i := len(hooks) - 1; i >= 0; i-- {
		fn := hooks[i]
		if fn == nil {
			continue
		}
		if err := b.runHook(ctx, fn); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// runHook invokes a single hook, converting a panic into an error so that a
// misbehaving hook cannot crash the whole application or leave the module in an
// inconsistent state.
func (b *BaseAppModule) runHook(ctx context.Context, fn HookFunc) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("appmod: hook panicked: %v", r)
		}
	}()

	return fn(ctx, b)
}

// BeforeStart registers a hook executed before the module is started.
func (b *BaseAppModule) BeforeStart(fn HookFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.beforeStartFns = append(b.beforeStartFns, fn)
}

// AfterStart registers a hook executed after the module is started.
func (b *BaseAppModule) AfterStart(fn HookFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.afterStartFns = append(b.afterStartFns, fn)
}

// BeforeDestroy registers a hook executed before the module is destroyed.
func (b *BaseAppModule) BeforeDestroy(fn HookFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.beforeDestroyFns = append(b.beforeDestroyFns, fn)
}

// AfterDestroy registers a hook executed after the module is destroyed.
func (b *BaseAppModule) AfterDestroy(fn HookFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.afterDestroyFns = append(b.afterDestroyFns, fn)
}

// DefaultConfig returns a default config (`App Module`, `v0.0.1`).
func DefaultConfig() Config {
	return Config{name: `App Module`, version: `v0.0.1`}
}

// NewConfig creates a new basic config.
func NewConfig(name, version string) Config {
	return Config{name: name, version: version}
}

// Option configures a [BaseAppModule] created with [New].
type Option func(*BaseAppModule)

// WithConfig sets the module configuration.
func WithConfig(config AppModuleConfig) Option {
	return func(b *BaseAppModule) { b.config = config }
}

// WithBeforeStart registers a BeforeStart hook.
func WithBeforeStart(fn HookFunc) Option {
	return func(b *BaseAppModule) { b.beforeStartFns = append(b.beforeStartFns, fn) }
}

// WithAfterStart registers an AfterStart hook.
func WithAfterStart(fn HookFunc) Option {
	return func(b *BaseAppModule) { b.afterStartFns = append(b.afterStartFns, fn) }
}

// WithBeforeDestroy registers a BeforeDestroy hook.
func WithBeforeDestroy(fn HookFunc) Option {
	return func(b *BaseAppModule) { b.beforeDestroyFns = append(b.beforeDestroyFns, fn) }
}

// WithAfterDestroy registers an AfterDestroy hook.
func WithAfterDestroy(fn HookFunc) Option {
	return func(b *BaseAppModule) { b.afterDestroyFns = append(b.afterDestroyFns, fn) }
}

// New creates a [BaseAppModule] configured with the given options.
func New(opts ...Option) *BaseAppModule {
	b := &BaseAppModule{}
	for _, opt := range opts {
		opt(b)
	}

	return b
}
