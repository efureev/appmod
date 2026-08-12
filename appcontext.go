package appmod

import (
	"context"
	"log/slog"
)

// AppContext bundles the shared services a [Manager] hands to its modules: the
// [Registry] for contract-based access between modules and the application
// logger.
//
// A single AppContext is created per [Manager] and injected into every
// registered module that implements [ContextAware] before the modules are
// started.
//
// Anything else modules need to share travels through the Registry, including
// an event bus: publish it once with [Provide] and take it with [Require]. The
// package deliberately has no bus of its own — see the package documentation.
type AppContext struct {
	// Registry is the shared service registry for Provide/Require contracts.
	Registry *Registry
	// Logger is the application logger (never nil).
	Logger *slog.Logger

	// shutdown is canceled when the application begins shutting down. It is the
	// observation side of the Manager's shutdown sequence, kept unexported so the
	// shutdown package stays an implementation detail rather than part of this
	// package's contract. It is nil in an AppContext assembled by hand.
	shutdown context.Context
}

// Done returns a channel closed when the application begins shutting down,
// letting a module react without waiting for its own Destroy — which only runs
// after every module that depends on it has already stopped:
//
//	go func() {
//		for {
//			select {
//			case <-m.AppContext().Done():
//				return // stop taking new work
//			case job := <-jobs:
//				process(job)
//			}
//		}
//	}()
//
// It closes for any teardown a [Manager] performs, including the rollback of a
// failed [Manager.Start]. An AppContext that was not created by a Manager never
// shuts down, and reports a nil channel — which blocks forever, the correct
// answer for "this application is not going down".
func (c *AppContext) Done() <-chan struct{} {
	if c == nil || c.shutdown == nil {
		return nil
	}

	return c.shutdown.Done()
}

// Context returns a context canceled when the application begins shutting down,
// for work that should be abandoned once the teardown starts. It is safe to
// derive request-scoped contexts from it: it is canceled for no other reason.
//
// It reports [context.Background] when no [Manager] created this AppContext.
func (c *AppContext) Context() context.Context {
	if c == nil || c.shutdown == nil {
		return context.Background()
	}

	return c.shutdown
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
