package appmod

import "errors"

// Lifecycle errors returned by [BaseAppModule].
var (
	// ErrAlreadyInitialized is returned by Init when the module has already
	// been initialized and not yet destroyed.
	ErrAlreadyInitialized = errors.New("appmod: module already initialized")
	// ErrNotInitialized is returned by Destroy when the module has not been
	// initialized (or has already been destroyed).
	ErrNotInitialized = errors.New("appmod: module is not initialized")
)

// Orchestration errors returned by [Manager].
var (
	// ErrEmptyName is returned by [Manager.Register] when the module name is empty.
	ErrEmptyName = errors.New("appmod: module name must not be empty")
	// ErrNilModule is returned by [Manager.Register] when the module is nil.
	ErrNilModule = errors.New("appmod: module must not be nil")
	// ErrDuplicateModule is returned by [Manager.Register] when a module with
	// the same name has already been registered.
	ErrDuplicateModule = errors.New("appmod: module already registered")
	// ErrUnknownDependency is returned when a module depends on a name that has
	// not been registered.
	ErrUnknownDependency = errors.New("appmod: unknown dependency")
	// ErrDependencyCycle is returned by [Manager.Start] when the dependency
	// graph contains a cycle.
	ErrDependencyCycle = errors.New("appmod: dependency cycle detected")
)
