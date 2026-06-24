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

// EventBus errors returned by [EventBus], [Subscribe] and [Publish].
var (
	// ErrNilBus is returned when a nil [EventBus] is passed to [Subscribe] or
	// [Publish].
	ErrNilBus = errors.New("appmod: event bus must not be nil")
	// ErrNilSubscriber is returned by [Subscribe] when the handler is nil.
	ErrNilSubscriber = errors.New("appmod: event subscriber must not be nil")
	// ErrBusClosed is returned by [Subscribe] and [Publish] after the bus has
	// been closed.
	ErrBusClosed = errors.New("appmod: event bus is closed")
)

// Registry errors returned by [Registry], [Provide] and [Require].
var (
	// ErrNilRegistry is returned when a nil [Registry] is passed to [Provide],
	// [Require] or [Revoke].
	ErrNilRegistry = errors.New("appmod: registry must not be nil")
	// ErrDuplicateProvider is returned by [Provide] when a contract of the same
	// type has already been provided.
	ErrDuplicateProvider = errors.New("appmod: contract already provided")
	// ErrProviderNotFound is returned by [Require] when no provider has been
	// registered for the requested contract.
	ErrProviderNotFound = errors.New("appmod: contract provider not found")
)
