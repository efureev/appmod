package appmod

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
