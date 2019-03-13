package appmod

// AppModuleConfig interface
type AppModuleConfig interface {
	Name() string
	Version() string
}

// AppModule interface
type AppModule interface {
	SetConfig(config AppModuleConfig)
	Config() AppModuleConfig

	Init() error
	Destroy() error

	BeforeStart(fn func(mod AppModule) error)
	//AfterStart(func(arg ...interface{})) error
	BeforeDestroy(fn func(mod AppModule) error)
	//AfterDestroy(func(arg ...interface{})) error
}

// Config structure
type Config struct {
	name    string
	version string
}

// Name returns app module name
func (c Config) Name() string {
	return c.name
}

// Version returns app module version
func (c Config) Version() string {
	return c.version
}

// BaseAppModule is abstract struct layer
type BaseAppModule struct {
	config          AppModuleConfig
	beforeStartFn   func(mod AppModule) error
	beforeDestroyFn func(mod AppModule) error
}

// Config returns app module config
func (b BaseAppModule) Config() AppModuleConfig {
	return b.config
}

// SetConfig set config to app module
func (b *BaseAppModule) SetConfig(config AppModuleConfig) {
	b.config = config
}

// Init initialize app module
func (b *BaseAppModule) Init() error {
	if b.beforeStartFn != nil {
		if err := b.beforeStartFn(b); err != nil {
			return err
		}
	}
	return nil
}

// Destroy app module
func (b *BaseAppModule) Destroy() error {
	if b.beforeDestroyFn != nil {
		if err := b.beforeDestroyFn(b); err != nil {
			return err
		}
	}
	return nil
}

// BeforeStart - event triggered run before init
func (b *BaseAppModule) BeforeStart(fn func(mod AppModule) error) {
	b.beforeStartFn = fn
}

// BeforeDestroy - event triggered run before destroy
func (b *BaseAppModule) BeforeDestroy(fn func(mod AppModule) error) {
	b.beforeDestroyFn = fn
}

// DefaultConfig return default config
func DefaultConfig() AppModuleConfig {
	return Config{`App Module`, `v0.0.1`}
}

// NewConfig create new basic config
func NewConfig(name, version string) AppModuleConfig {
	return Config{name, version}
}
