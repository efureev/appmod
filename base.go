package appmod

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// discardLogger is the shared no-op logger used by modules without one. It is a
// package-level value because log() is called several times per lifecycle and
// building a fresh slog.Logger each time is pure waste.
var discardLogger = slog.New(slog.DiscardHandler)

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

	// cleanups are the compensations registered while the module was starting;
	// they are consumed (and cleared) by the teardown.
	cleanups []CleanupFunc

	state State
}

// AddCleanup registers fn to run when the module is torn down: after the
// BeforeDestroy hooks during [BaseAppModule.Destroy], and during the rollback of
// a failed [BaseAppModule.Init]. Cleanups run in reverse registration order
// (LIFO) and are cleared as they run, so each executes at most once and a module
// that is initialized again starts with an empty list.
//
// It exists because the natural place to release something is next to where it
// was acquired, not in a separate teardown hook wired up by hand. The canonical
// case is a subscription to an external event bus, whose unsubscribe function
// would otherwise have nowhere to live: left unregistered, the handler outlives
// the module, so a module stopped and started again is subscribed twice.
//
// A cleanup that panics is recovered and reported as an error, like a hook. A
// nil fn is ignored.
func (b *BaseAppModule) AddCleanup(fn CleanupFunc) {
	if fn == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.cleanups = append(b.cleanups, fn)
}

// runCleanups executes and clears the registered cleanups in reverse
// registration order, collecting every error rather than stopping at the first
// so that no compensation is skipped.
func (b *BaseAppModule) runCleanups(ctx context.Context) []error {
	b.mu.Lock()
	cleanups := b.cleanups
	b.cleanups = nil
	b.mu.Unlock()

	var errs []error
	for i := len(cleanups) - 1; i >= 0; i-- {
		if err := b.runCleanup(ctx, cleanups[i]); err != nil {
			errs = append(errs, fmt.Errorf("appmod: module %q: cleanup #%d failed: %w", b.Name(), i, err))
		}
	}

	return errs
}

// runCleanup invokes a single cleanup, converting a panic into an error so a
// misbehaving cleanup cannot crash the teardown.
func (b *BaseAppModule) runCleanup(ctx context.Context, fn CleanupFunc) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("cleanup panicked: %v", r)
		}
	}()

	return fn(ctx)
}

// State returns the current lifecycle state of the module.
func (b *BaseAppModule) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state
}

// Config returns the app module config.
//
// A module that has never been configured — including the zero value, which the
// type documents as ready to use — reports [DefaultConfig] rather than nil.
// Returning nil made the package's own idiom, Config().Name() inside a hook,
// a nil dereference.
func (b *BaseAppModule) Config() AppModuleConfig {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.config == nil {
		return DefaultConfig()
	}

	return b.config
}

// SetConfig sets the config of the app module.
func (b *BaseAppModule) SetConfig(config AppModuleConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.config = config
}

// Name returns the module name. It is a shortcut for Config().Name() and follows
// it exactly, so an unconfigured module reports the [DefaultConfig] name rather
// than an empty string.
func (b *BaseAppModule) Name() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.config == nil {
		return DefaultConfig().Name()
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
// shared [Registry] (Registry) and the application logger (Logger).
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
		return discardLogger
	}

	return logger
}

// Init initializes the app module.
//
// It runs all BeforeStart hooks, then all AfterStart hooks, and only once both
// phases have succeeded does it transition the module to [StateRunning]. Init
// can only be started from [StateCreated], [StateDestroyed] or [StateFailed];
// otherwise it returns [ErrAlreadyInitialized].
//
// The module therefore reports [StateInitializing] for the whole duration of
// Init, including while the AfterStart hooks run. This is deliberate: a module
// that has not finished starting must not be observable as running, otherwise a
// concurrent [BaseAppModule.Destroy] would pass its state guard and tear the
// module down while Init is still working on it (running the teardown hooks a
// second time, concurrently with the rollback below).
//
// Init is context-aware: the context is checked before every start hook and a
// canceled context aborts the remaining hooks.
//
// Failure semantics are atomic: if any start hook (BeforeStart or AfterStart)
// fails or the context is canceled, Init automatically rolls back and leaves the
// module in [StateFailed]. The module is therefore never left half-started: Init
// either fully succeeds ([StateRunning]) or fails ([StateFailed]). Any rollback
// error is joined with the original cause via [errors.Join]. A failing hook is
// reported as a [HookError].
//
// The rollback unwinds the cleanups registered with [BaseAppModule.AddCleanup],
// not the teardown hooks: teardown hooks describe how to stop a module that
// finished starting, and running them after a half-finished start would undo
// work that never happened. A start hook that acquires something must therefore
// register its release with AddCleanup for that release to happen on a failed
// start.
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
	afterStart := slices.Clone(b.afterStartHooks)
	b.mu.Unlock()

	if err := b.runPhase(ctx, PhaseAfterStart, afterStart); err != nil {
		logger.ErrorContext(ctx, "module init failed", "module", b.Name(), "phase", PhaseAfterStart.String(), "error", err)
		return b.failInit(ctx, err)
	}

	// Publish StateRunning only now: until both start phases have completed the
	// module must not be observable as running (see the doc comment above).
	b.setState(StateRunning)

	logger.InfoContext(ctx, "module initialized", "module", b.Name(), "duration", time.Since(start))

	return nil
}

// failInit performs the rollback for a failed Init, marks the module as
// [StateFailed] and returns the (possibly joined) error.
func (b *BaseAppModule) failInit(ctx context.Context, cause error) error {
	// Teardown hooks must observe the same state no matter which path reached
	// them. Leaving the module in StateInitializing here meant a hook that
	// branches on m.State() behaved one way under Destroy and another under
	// rollback — and "Initializing" is a plainly wrong answer for cleanup code.
	b.setState(StateDestroying)

	if rbErr := b.rollback(ctx); rbErr != nil {
		cause = errors.Join(cause, rbErr)
	}
	b.setState(StateFailed)

	return cause
}

// rollback compensates a failed Init by unwinding the cleanups registered with
// [BaseAppModule.AddCleanup], in reverse registration order.
//
// It runs exactly the compensations for the work that actually happened: a start
// hook registers its cleanup after it succeeds, so a hook that never ran, or ran
// and failed, has nothing registered and nothing is undone on its behalf.
//
// The teardown hooks are deliberately NOT run here. They describe how to stop a
// module that finished starting, which is a different job: invoking them after a
// half-finished start meant closing a pool that was never opened or a listener
// that was never bound, and the package could not tell the difference because it
// pairs no start hook with a specific teardown hook. Compensation has to be
// registered by the code that did the work — hence AddCleanup.
//
// Unlike the start phases, rollback does not abort on context cancellation: it
// always attempts every cleanup and joins all resulting errors so that nothing
// is skipped.
func (b *BaseAppModule) rollback(ctx context.Context) error {
	return errors.Join(b.runCleanups(ctx)...)
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
//
// Any cleanups registered with [BaseAppModule.AddCleanup] run between the two
// hook phases — after BeforeDestroy has had its say, before the module is marked
// destroyed — in reverse registration order. Their errors are joined with
// whatever the AfterDestroy phase reports.
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

	errs := b.runCleanups(ctx)
	for _, err := range errs {
		logger.ErrorContext(ctx, "module cleanup failed", "module", b.Name(), "error", err)
	}

	b.mu.Lock()
	b.state = StateDestroyed
	afterDestroy := slices.Clone(b.afterDestroyHooks)
	b.mu.Unlock()

	if err := b.runPhase(ctx, PhaseAfterDestroy, afterDestroy); err != nil {
		logger.ErrorContext(ctx, "module destroy failed", "module", b.Name(), "phase", PhaseAfterDestroy.String(), "error", err)
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
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

// runHook invokes a single hook, converting a panic into an error so that a
// misbehaving hook cannot crash the whole application or leave the module in an
// inconsistent state.
func (b *BaseAppModule) runHook(ctx context.Context, fn HookFunc) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("hook panicked: %v", r)
		}
	}()

	return fn(ctx, hookView{mod: b})
}

// hookView is the narrow view of a module handed to a [HookFunc].
//
// It is a distinct type rather than the module itself, and that is the whole
// point: passing *BaseAppModule would satisfy [HookModule] just as well, but a
// single type assertion back to [Lifecycle] or [HookRegistry] would then let a
// hook re-enter the lifecycle or mutate the hook set while it is running. The
// narrowing has to be unforgeable to mean anything.
type hookView struct{ mod *BaseAppModule }

// Config returns the module configuration.
func (v hookView) Config() AppModuleConfig { return v.mod.Config() }

// SetConfig sets the module configuration.
func (v hookView) SetConfig(config AppModuleConfig) { v.mod.SetConfig(config) }

// Name returns the module name.
func (v hookView) Name() string { return v.mod.Name() }

// State returns the current lifecycle state.
func (v hookView) State() State { return v.mod.State() }

// orderHooks returns a copy of the hooks sorted by ascending priority. Hooks
// with the same priority keep their registration order (the sort is stable).
//
// The comparison uses [cmp.Compare] rather than a subtraction: the difference of
// two extreme priorities (for example math.MinInt and math.MaxInt) overflows int
// and silently inverts the order.
func orderHooks(hooks []Hook) []Hook {
	ordered := slices.Clone(hooks)
	slices.SortStableFunc(ordered, func(a, b Hook) int {
		return cmp.Compare(a.Priority, b.Priority)
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
//
// It panics if phase is not one of the four defined phases. An out-of-range
// phase is a programmer error, and dropping the hook silently — the previous
// behavior — turned it into start-up logic that simply never ran, with nothing
// anywhere to notice. Check untrusted values with [Phase.Valid] first.
func (b *BaseAppModule) AddHook(phase Phase, hook Hook) {
	mustValidPhase("AddHook", phase)

	b.mu.Lock()
	defer b.mu.Unlock()

	slice := b.hooksFor(phase)
	*slice = append(*slice, hook)
}

// mustValidPhase panics unless phase is one of the four defined phases. The
// caller name is included so the panic points at the offending API.
func mustValidPhase(op string, phase Phase) {
	if !phase.Valid() {
		panic(fmt.Sprintf("appmod: %s: invalid phase %s", op, phase))
	}
}

// RemoveHook removes the named hook from the given phase and reports whether a
// hook was removed. Anonymous hooks (empty name) are never removed.
//
// Like [BaseAppModule.AddHook] it panics on a phase that is not one of the four
// defined ones: a false return must mean "no such hook", never "no such phase".
func (b *BaseAppModule) RemoveHook(phase Phase, name string) bool {
	mustValidPhase("RemoveHook", phase)

	if name == "" {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	slice := b.hooksFor(phase)

	// slices.DeleteFunc zeroes the freed tail, so the removed hooks (and whatever
	// their closures captured) become unreachable. Compacting by hand with
	// (*slice)[:0] left those references live in the backing array.
	before := len(*slice)
	*slice = slices.DeleteFunc(*slice, func(h Hook) bool { return h.Name == name })

	return len(*slice) != before
}

// hooksFor returns a pointer to the hook slice for the given phase. The caller
// must hold mu, and must have validated the phase with mustValidPhase.
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
		// Unreachable: every caller validates the phase first. Panicking rather
		// than returning nil means a phase added later without wiring it up here
		// fails loudly instead of silently dropping hooks — the very bug the
		// validation was introduced to kill.
		panic(fmt.Sprintf("appmod: unreachable: hooksFor(%s)", phase))
	}
}
