package appmod

// AppModuleConfig describes module configuration.
type AppModuleConfig interface {
	// Name returns the app module name.
	Name() string
	// Version returns the app module version.
	Version() string
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

// DefaultConfig returns a default config (`App Module`, `v0.0.1`).
func DefaultConfig() Config {
	return Config{name: `App Module`, version: `v0.0.1`}
}

// NewConfig creates a new basic config.
func NewConfig(name, version string) Config {
	return Config{name: name, version: version}
}
