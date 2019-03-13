package appmod

type AppModuleConfig interface {
	Name() string
	Version() string
}

type AppModuleEvent *func(arg ...interface{})

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

type Config struct {
	name    string
	version string
}

func (c Config) Name() string {
	return c.name
}

func (c Config) Version() string {
	return c.version
}

type BaseAppModule struct {
	config          AppModuleConfig
	beforeStartFn   func(mod AppModule) error
	beforeDestroyFn func(mod AppModule) error
}

func (h BaseAppModule) Config() AppModuleConfig {
	return h.config
}

func (h *BaseAppModule) SetConfig(config AppModuleConfig) {
	h.config = config
}

func (b *BaseAppModule) Init() error {
	if b.beforeStartFn != nil {
		if err := b.beforeStartFn(b); err != nil {
			return err
		}
	}
	return nil
}

func (b *BaseAppModule) Destroy() error {
	if b.beforeDestroyFn != nil {
		if err := b.beforeDestroyFn(b); err != nil {
			return err
		}
	}
	return nil
}

func (b *BaseAppModule) BeforeStart(fn func(mod AppModule) error) {
	b.beforeStartFn = fn
}
func (b *BaseAppModule) BeforeDestroy(fn func(mod AppModule) error) {
	b.beforeDestroyFn = fn
}

func DefaultConfig() AppModuleConfig {
	return Config{`App Module`, `v0.0.1`}
}

func NewConfig(name, version string) AppModuleConfig {
	return Config{name, version}
}
