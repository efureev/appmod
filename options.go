package appmod

import "log/slog"

// Option configures a [BaseAppModule] created with [New].
type Option func(*BaseAppModule)

// WithConfig sets the module configuration.
func WithConfig(config AppModuleConfig) Option {
	return func(b *BaseAppModule) { b.config = config }
}

// WithModuleLogger sets the structured logger used to report the module's
// lifecycle events.
func WithModuleLogger(logger *slog.Logger) Option {
	return func(b *BaseAppModule) { b.logger = logger }
}

// WithBeforeStart registers an anonymous BeforeStart hook.
func WithBeforeStart(fn HookFunc) Option {
	return func(b *BaseAppModule) { b.beforeStartHooks = append(b.beforeStartHooks, Hook{Run: fn}) }
}

// WithAfterStart registers an anonymous AfterStart hook.
func WithAfterStart(fn HookFunc) Option {
	return func(b *BaseAppModule) { b.afterStartHooks = append(b.afterStartHooks, Hook{Run: fn}) }
}

// WithBeforeDestroy registers an anonymous BeforeDestroy hook.
func WithBeforeDestroy(fn HookFunc) Option {
	return func(b *BaseAppModule) { b.beforeDestroyHooks = append(b.beforeDestroyHooks, Hook{Run: fn}) }
}

// WithAfterDestroy registers an anonymous AfterDestroy hook.
func WithAfterDestroy(fn HookFunc) Option {
	return func(b *BaseAppModule) { b.afterDestroyHooks = append(b.afterDestroyHooks, Hook{Run: fn}) }
}

// WithHook registers a named, prioritized hook for the given phase.
func WithHook(phase Phase, hook Hook) Option {
	return func(b *BaseAppModule) {
		if slice := b.hooksFor(phase); slice != nil {
			*slice = append(*slice, hook)
		}
	}
}

// New creates a [BaseAppModule] configured with the given options.
func New(opts ...Option) *BaseAppModule {
	b := &BaseAppModule{}
	for _, opt := range opts {
		opt(b)
	}

	return b
}
