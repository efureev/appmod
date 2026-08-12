// Package hubmod ties a msghub event bus to the appmod module lifecycle.
//
// appmod ships no bus of its own: answering a request and broadcasting a fact
// are different mechanisms with different failure modes, and the [appmod.Registry]
// is the single place through which modules share anything. This package is the
// glue for the bus this project maintains,
// github.com/efureev/msghub, and provides the three things that glue needs:
//
//   - [Module] owns a [msghub.Hub] and gives it a lifecycle, draining and closing it
//     when the application goes down.
//   - [SubscribeModule] scopes a subscription to a module, so a module that is
//     stopped and started again is not subscribed twice.
//   - [Provide] and [Require] publish the hub through the registry, so any module
//     can reach it without depending on whoever created it.
//
// It is a separate Go module. Importing appmod does not pull msghub in, and
// importing msghub does not pull appmod in; only a program that wants both
// depends on both.
//
//	bus := msghub.New()
//	mgr := appmod.NewManager()
//
//	must(mgr.Register("bus", hubmod.NewModule(bus)))
//	must(mgr.Register("cache", newCache(), "bus"))
//
// A module then takes the hub from the registry in a start hook and subscribes:
//
//	h, err := hubmod.Require(m.AppContext().Registry)
//	if err != nil {
//	    return err
//	}
//
//	return hubmod.SubscribeModule(&m.BaseAppModule, h, UserCreated, m.onUserCreated)
package hubmod

import (
	"context"
	"errors"

	"github.com/efureev/appmod/v4"
	"github.com/efureev/msghub/v3"
)

// Errors reported by this package.
var (
	// ErrNilHub is returned when a nil hub is passed to [NewModule], [Provide]
	// or [SubscribeModule].
	ErrNilHub = errors.New("hubmod: hub must not be nil")
	// ErrNilModule is returned by [SubscribeModule] when the module is nil.
	ErrNilModule = errors.New("hubmod: module must not be nil")
	// ErrNoAppContext is returned by [SubscribeModule] when the module has no
	// [appmod.AppContext] yet. A [appmod.Manager] injects it before starting its
	// modules, so subscribe from a start hook rather than from a constructor.
	ErrNoAppContext = errors.New("hubmod: module has no app context")
)

// Module owns a [msghub.Hub] and gives it an appmod lifecycle.
//
// Registering it as a module rather than creating the hub inline buys the
// teardown: when the application stops, the hub is drained and closed in
// dependency order, after every module that publishes to it has already
// stopped. Declare a dependency on this module from anything that uses the bus,
// and the ordering follows.
type Module struct {
	*appmod.BaseAppModule

	hub      *msghub.Hub
	drain    bool
	provided bool
}

// Compile-time proof that the adapter satisfies the appmod contracts.
var (
	_ appmod.AppModule    = (*Module)(nil)
	_ appmod.ContextAware = (*Module)(nil)
)

// ModuleOption configures a [Module].
type ModuleOption func(*Module)

// WithoutDrain stops the module from draining the hub before closing it, so
// teardown abandons whatever is still queued instead of waiting for it.
//
// Draining is the default because a queued event is usually work the
// application still owes. Turn it off for streams where a late delivery is
// worth less than a prompt shutdown — progress updates, metrics — or where a
// handler may block long enough to hit the manager's shutdown timeout.
func WithoutDrain() ModuleOption {
	return func(m *Module) { m.drain = false }
}

// WithoutRegistry stops the module from publishing the hub through the shared
// [appmod.Registry] when it starts.
//
// Use it when the application wires the hub into its modules itself, or when it
// runs several hubs and a single `*msghub.Hub` contract would collide: the registry
// keys by type, so the second [Provide] of the same type reports
// [appmod.ErrDuplicateProvider].
func WithoutRegistry() ModuleOption {
	return func(m *Module) { m.provided = false }
}

// NewModule wraps h as an appmod module named "hub".
//
// By default the module publishes the hub through the shared registry when it
// starts, revokes it when it stops, and drains the hub before closing it. The
// appmod options are applied after the defaults, so [appmod.WithConfig] can
// rename it — useful when an application runs more than one hub.
//
// It panics if h is nil: a nil hub is a wiring mistake in a constructor with
// nothing to report an error to, and every later call would fail anyway.
func NewModule(h *msghub.Hub, opts ...ModuleOption) *Module {
	if h == nil {
		panic(ErrNilHub)
	}

	m := &Module{hub: h, drain: true, provided: true}

	for _, apply := range opts {
		apply(m)
	}

	m.BaseAppModule = appmod.New(appmod.WithConfig(appmod.NewConfig("hub", "v3")))

	if m.provided {
		m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
			return Provide(m.registry(), m.hub)
		})
		m.AddCleanup(func(context.Context) error {
			_, err := appmod.Revoke[*msghub.Hub](m.registry())

			return err
		})
	}

	// AfterDestroy, not BeforeDestroy: appmod runs BeforeDestroy hooks, then
	// cleanups, then AfterDestroy, and a failing BeforeDestroy puts the module
	// back into Running with its cleanups unrun. Shutting the hub down last
	// means the registry entry is already revoked by then, and a drain that
	// times out is reported from a module that did stop rather than leaving a
	// half-stopped one behind.
	m.AfterDestroy(func(ctx context.Context, _ appmod.HookModule) error {
		return m.shutdown(ctx)
	})

	return m
}

// Configure applies appmod options to the underlying module, for callers that
// need to rename it or attach their own hooks.
func (m *Module) Configure(opts ...appmod.Option) *Module {
	for _, apply := range opts {
		apply(m.BaseAppModule)
	}

	return m
}

// Hub returns the bus this module owns.
func (m *Module) Hub() *msghub.Hub { return m.hub }

// registry returns the shared registry, or nil when no [appmod.AppContext] has
// been injected. Provide and Revoke both report a nil registry themselves.
func (m *Module) registry() *appmod.Registry {
	ac := m.AppContext()
	if ac == nil {
		return nil
	}

	return ac.Registry
}

// shutdown drains and closes the hub, bounded by the teardown context.
//
// A drain that times out is reported but does not stop the close: leaving the
// subscriber goroutines running would be worse than losing the events that were
// still queued, and the caller learns about both from the joined error.
func (m *Module) shutdown(ctx context.Context) error {
	var errs []error

	if m.drain {
		if err := m.hub.Drain(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if err := m.hub.Close(ctx); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// Provide publishes h through the registry as the `*msghub.Hub` contract.
//
// It reports [appmod.ErrDuplicateProvider] if a hub has already been provided,
// which is what happens when two [Module] instances run with the registry
// enabled. Give all but one [WithoutRegistry].
func Provide(r *appmod.Registry, h *msghub.Hub) error {
	if h == nil {
		return ErrNilHub
	}

	return appmod.Provide[*msghub.Hub](r, h)
}

// Require returns the hub previously published with [Provide].
//
// Call it from a start hook of a module that declares a dependency on the
// module owning the hub; the provider's AfterStart has run by then. Called
// earlier it reports [appmod.ErrProviderNotFound].
func Require(r *appmod.Registry) (*msghub.Hub, error) {
	return appmod.Require[*msghub.Hub](r)
}

// SubscribeModule registers fn as a handler on h for topic t and ties the
// subscription to m's lifecycle: it is removed when the module is destroyed, and
// when a failed [appmod.BaseAppModule.Init] rolls back.
//
// Prefer it to calling [msghub.Subscribe] directly from inside a module. Subscribe
// hands back a handle the caller has to store somewhere and remember to close;
// when that does not happen the handler outlives the module, so a module that is
// stopped and started again is subscribed twice and every event is delivered
// twice — growing by one delivery per restart, while the stale handler keeps a
// live reference to the destroyed module.
//
// It returns [ErrNilModule] if m is nil, [ErrNilHub] if h is nil and
// [ErrNoAppContext] if no [appmod.AppContext] has been injected yet. A
// [appmod.Manager] injects it before starting its modules, so call this from a
// start hook rather than from a constructor.
func SubscribeModule[T any](
	m *appmod.BaseAppModule,
	h *msghub.Hub,
	t msghub.Topic[T],
	fn msghub.Handler[T],
	opts ...msghub.SubOption,
) error {
	if m == nil {
		return ErrNilModule
	}
	if h == nil {
		return ErrNilHub
	}
	if m.AppContext() == nil {
		return ErrNoAppContext
	}

	sub, err := msghub.Subscribe(h, t, fn, opts...)
	if err != nil {
		return err
	}

	m.AddCleanup(func(context.Context) error {
		sub.Close()

		return nil
	})

	return nil
}
