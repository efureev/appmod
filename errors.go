package appmod

import (
	"errors"

	shutdown "github.com/efureev/go-shutdown/v3"
)

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
	// ErrAlreadyRun is returned by the second [Manager.Run] on a manager. The
	// shutdown sequence runs exactly once, so a second Run would otherwise start
	// the modules and return immediately without ever stopping them.
	ErrAlreadyRun = errors.New("appmod: manager already run")
	// ErrShutdownTimeout is returned by [Manager.Run], wrapped, when the teardown
	// exceeds [WithShutdownTimeout]. It is re-exported from the shutdown package
	// so callers can check for it without importing that package themselves, and
	// it wraps [context.DeadlineExceeded].
	ErrShutdownTimeout = shutdown.ErrTimeout
	// ErrAlreadyStarted is returned by [Manager.Start] when the manager is
	// already starting or running, or when a previous teardown did not finish.
	// Starting twice is refused instead of attempted, because a second Start
	// would fail on the already-initialized modules and then roll back — tearing
	// down the modules the first Start had brought up.
	ErrAlreadyStarted = errors.New("appmod: manager already started")
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
	// ErrNoAppContext is returned by [SubscribeModule] when the module has not
	// been given an [AppContext] yet. A [Manager] injects it before starting its
	// modules, so subscribe from a start hook rather than from a constructor.
	ErrNoAppContext = errors.New("appmod: module has no app context")
)

// Registry errors returned by [Registry], [Provide] and [Require].
var (
	// ErrNilRegistry is returned when a nil [Registry] is passed to [Provide],
	// [Require] or [Revoke].
	ErrNilRegistry = errors.New("appmod: registry must not be nil")
	// ErrDuplicateProvider is returned by [Provide] when a contract of the same
	// type has already been provided.
	ErrDuplicateProvider = errors.New("appmod: contract already provided")
	// ErrNilImplementation is returned by [Provide] when the implementation is
	// nil, which would otherwise only surface as a panic inside [Require].
	ErrNilImplementation = errors.New("appmod: contract implementation must not be nil")
	// ErrProviderNotFound is returned by [Require] when no provider has been
	// registered for the requested contract.
	ErrProviderNotFound = errors.New("appmod: contract provider not found")
)
