package appmod

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

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
