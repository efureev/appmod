package appmod

import "log/slog"

// AppContext bundles the shared services a [Manager] hands to its modules: the
// [EventBus] for fire-and-forget notifications (push), the [Registry] for
// contract-based request/response access between modules (pull) and the
// application logger.
//
// A single AppContext is created per [Manager] and injected into every
// registered module that implements [ContextAware] before the modules are
// started.
type AppContext struct {
	// Bus is the shared event bus for publish/subscribe notifications.
	Bus *EventBus
	// Registry is the shared service registry for Provide/Require contracts.
	Registry *Registry
	// Logger is the application logger (never nil).
	Logger *slog.Logger
}

// ContextAware is an optional capability of a module. A [Manager] injects its
// shared [AppContext] into every registered module that implements it, before
// starting them.
//
// [BaseAppModule] implements ContextAware: the injected context is stored and
// can be retrieved with [BaseAppModule.AppContext].
type ContextAware interface {
	// SetAppContext receives the shared application context.
	SetAppContext(*AppContext)
}
