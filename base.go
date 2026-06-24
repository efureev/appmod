package appmod

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// BaseAppModule is an abstract, embeddable base implementation of [AppModule].
//
// The zero value is ready to use and safe for concurrent use by multiple
// goroutines.
type BaseAppModule struct {
	mu sync.Mutex

	config AppModuleConfig
	logger *slog.Logger
	appCtx *AppContext

	beforeStartHooks   []Hook
	afterStartHooks    []Hook
	beforeDestroyHooks []Hook
	afterDestroyHooks  []Hook

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

// Name returns the module name (a shortcut for Config().Name()). It returns an
// empty string when no configuration has been set.
func (b *BaseAppModule) Name() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.config == nil {
		return ""
	}

	return b.config.Name()
}

// SetLogger sets the structured logger used to report this module's lifecycle
// events. A nil logger disables logging.
func (b *BaseAppModule) SetLogger(logger *slog.Logger) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.logger = logger
}

// SetAppContext stores the shared [AppContext] injected by a [Manager]. It makes
// *BaseAppModule satisfy [ContextAware].
func (b *BaseAppModule) SetAppContext(c *AppContext) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.appCtx = c
}

// AppContext returns the shared [AppContext] previously injected with
// [BaseAppModule.SetAppContext], or nil if none was set. Use it to reach the
// shared [EventBus] (Bus) and [Registry] (Registry).
func (b *BaseAppModule) AppContext() *AppContext {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.appCtx
}

// log returns the configured logger or a no-op one. The caller must not hold mu.
func (b *BaseAppModule) log() *slog.Logger {
	b.mu.Lock()
	logger := b.logger
	b.mu.Unlock()

	if logger == nil {
		return slog.New(slog.DiscardHandler)
	}

	return logger
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
// is joined with the original cause via [errors.Join]. A failing hook is
// reported as a [HookError].
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
	beforeStart := slices.Clone(b.beforeStartHooks)
	b.mu.Unlock()

	logger := b.log()
	start := time.Now()
	logger.DebugContext(ctx, "module init started", "module", b.Name())

	if err := b.runPhase(ctx, PhaseBeforeStart, beforeStart); err != nil {
		logger.ErrorContext(ctx, "module init failed", "module", b.Name(), "phase", PhaseBeforeStart.String(), "error", err)
		return b.failInit(ctx, err)
	}

	b.mu.Lock()
	b.state = StateRunning
	afterStart := slices.Clone(b.afterStartHooks)
	b.mu.Unlock()

	if err := b.runPhase(ctx, PhaseAfterStart, afterStart); err != nil {
		logger.ErrorContext(ctx, "module init failed", "module", b.Name(), "phase", PhaseAfterStart.String(), "error", err)
		return b.failInit(ctx, err)
	}

	logger.InfoContext(ctx, "module initialized", "module", b.Name(), "duration", time.Since(start))

	return nil
}

// failInit performs the rollback for a failed Init, marks the module as
// [StateFailed] and returns the (possibly joined) error.
func (b *BaseAppModule) failInit(ctx context.Context, cause error) error {
	if rbErr := b.rollback(ctx); rbErr != nil {
		cause = errors.Join(cause, rbErr)
	}
	b.setState(StateFailed)

	return cause
}

// rollback compensates a failed Init by running the teardown hooks
// (BeforeDestroy then AfterDestroy) in reverse order, so that resources
// acquired by the start hooks that already ran are released.
//
// Unlike the start phases, rollback does not abort on context cancellation: it
// always attempts every teardown hook and joins all resulting errors so that no
// cleanup is skipped.
func (b *BaseAppModule) rollback(ctx context.Context) error {
	b.mu.Lock()
	beforeDestroy := slices.Clone(b.beforeDestroyHooks)
	afterDestroy := slices.Clone(b.afterDestroyHooks)
	b.mu.Unlock()

	var errs []error
	errs = append(errs, b.runTeardown(ctx, PhaseBeforeDestroy, beforeDestroy)...)
	errs = append(errs, b.runTeardown(ctx, PhaseAfterDestroy, afterDestroy)...)

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
// is marked destroyed), the error is returned as a [HookError] and the module
// stays in [StateRunning] so that Destroy can be retried.
func (b *BaseAppModule) Destroy(ctx context.Context) error {
	b.mu.Lock()
	if b.state != StateRunning {
		b.mu.Unlock()
		return ErrNotInitialized
	}
	b.state = StateDestroying
	beforeDestroy := slices.Clone(b.beforeDestroyHooks)
	b.mu.Unlock()

	logger := b.log()
	start := time.Now()
	logger.DebugContext(ctx, "module destroy started", "module", b.Name())

	if err := b.runPhase(ctx, PhaseBeforeDestroy, beforeDestroy); err != nil {
		b.setState(StateRunning)
		logger.ErrorContext(ctx, "module destroy failed", "module", b.Name(), "phase", PhaseBeforeDestroy.String(), "error", err)
		return err
	}

	b.mu.Lock()
	b.state = StateDestroyed
	afterDestroy := slices.Clone(b.afterDestroyHooks)
	b.mu.Unlock()

	if err := b.runPhase(ctx, PhaseAfterDestroy, afterDestroy); err != nil {
		logger.ErrorContext(ctx, "module destroy failed", "module", b.Name(), "phase", PhaseAfterDestroy.String(), "error", err)
		return err
	}

	logger.InfoContext(ctx, "module destroyed", "module", b.Name(), "duration", time.Since(start))

	return nil
}

// setState atomically updates the internal lifecycle state.
func (b *BaseAppModule) setState(s State) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = s
}

// runPhase executes the given hooks in priority order, stopping at the first
// error. A failing (or panicking) hook is wrapped in a [HookError].
//
// The context is checked before each hook so that a canceled or timed-out
// context aborts the remaining hooks with the context error.
func (b *BaseAppModule) runPhase(ctx context.Context, phase Phase, hooks []Hook) error {
	for i, h := range orderHooks(hooks) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if h.Run == nil {
			continue
		}
		if err := b.runHook(ctx, h.Run); err != nil {
			return &HookError{Phase: phase, Index: i, Name: h.Name, Module: b.Name(), Err: err}
		}
	}

	return nil
}

// runTeardown executes the given teardown hooks in reverse priority order,
// collecting every error instead of stopping at the first one. It is used by
// [BaseAppModule.rollback] and intentionally ignores context cancellation so
// that cleanup always runs to completion.
func (b *BaseAppModule) runTeardown(ctx context.Context, phase Phase, hooks []Hook) []error {
	ordered := orderHooks(hooks)

	var errs []error
	for i := len(ordered) - 1; i >= 0; i-- {
		h := ordered[i]
		if h.Run == nil {
			continue
		}
		if err := b.runHook(ctx, h.Run); err != nil {
			errs = append(errs, &HookError{Phase: phase, Index: i, Name: h.Name, Module: b.Name(), Err: err})
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
			err = fmt.Errorf("hook panicked: %v", r)
		}
	}()

	return fn(ctx, b)
}

// orderHooks returns a copy of the hooks sorted by ascending priority. Hooks
// with the same priority keep their registration order (the sort is stable).
func orderHooks(hooks []Hook) []Hook {
	ordered := slices.Clone(hooks)
	slices.SortStableFunc(ordered, func(a, b Hook) int {
		return a.Priority - b.Priority
	})

	return ordered
}

// BeforeStart registers an anonymous hook executed before the module is started.
func (b *BaseAppModule) BeforeStart(fn HookFunc) {
	b.AddHook(PhaseBeforeStart, Hook{Run: fn})
}

// AfterStart registers an anonymous hook executed after the module is started.
func (b *BaseAppModule) AfterStart(fn HookFunc) {
	b.AddHook(PhaseAfterStart, Hook{Run: fn})
}

// BeforeDestroy registers an anonymous hook executed before the module is destroyed.
func (b *BaseAppModule) BeforeDestroy(fn HookFunc) {
	b.AddHook(PhaseBeforeDestroy, Hook{Run: fn})
}

// AfterDestroy registers an anonymous hook executed after the module is destroyed.
func (b *BaseAppModule) AfterDestroy(fn HookFunc) {
	b.AddHook(PhaseAfterDestroy, Hook{Run: fn})
}

// AddHook registers a named, prioritized hook for the given phase. Within a
// phase, hooks run in ascending priority order; hooks of equal priority keep
// their registration order.
func (b *BaseAppModule) AddHook(phase Phase, hook Hook) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if slice := b.hooksFor(phase); slice != nil {
		*slice = append(*slice, hook)
	}
}

// RemoveHook removes the named hook from the given phase and reports whether a
// hook was removed. Anonymous hooks (empty name) are never removed.
func (b *BaseAppModule) RemoveHook(phase Phase, name string) bool {
	if name == "" {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	slice := b.hooksFor(phase)
	if slice == nil {
		return false
	}

	kept := (*slice)[:0]
	removed := false
	for _, h := range *slice {
		if h.Name == name {
			removed = true
			continue
		}
		kept = append(kept, h)
	}
	*slice = kept

	return removed
}

// hooksFor returns a pointer to the hook slice for the given phase, or nil for
// an unknown phase. The caller must hold mu.
func (b *BaseAppModule) hooksFor(phase Phase) *[]Hook {
	switch phase {
	case PhaseBeforeStart:
		return &b.beforeStartHooks
	case PhaseAfterStart:
		return &b.afterStartHooks
	case PhaseBeforeDestroy:
		return &b.beforeDestroyHooks
	case PhaseAfterDestroy:
		return &b.afterDestroyHooks
	default:
		return nil
	}
}
