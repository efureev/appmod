package appmod

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

// Unsubscribe removes a subscription previously created with [Subscribe]. It is
// safe to call more than once; subsequent calls are no-ops.
type Unsubscribe func()

// subscriber is a single registered handler for an event type.
type subscriber struct {
	id uint64
	fn func(ctx context.Context, ev any) error
}

// EventBus is a small, dependency-free, type-safe publish/subscribe bus for
// loosely-coupled, fire-and-forget communication between modules.
//
// Events are keyed by their Go type: a subscriber registered for T (via the
// generic [Subscribe]) is invoked for every value of type T published with the
// generic [Publish]. Delivery is synchronous — Publish returns only after every
// subscriber has run — which keeps ordering and error handling predictable and
// testable. A panicking subscriber is recovered and turned into an error rather
// than crashing the publisher.
//
// An EventBus is safe for concurrent use by multiple goroutines.
//
// Use the bus for notifications ("cache invalidated", "config reloaded"), not
// for request/response data access between modules — for the latter use the
// [Registry] ([Provide]/[Require]).
type EventBus struct {
	mu     sync.RWMutex
	nextID uint64
	subs   map[reflect.Type][]subscriber
	closed bool
}

// NewEventBus creates an empty [EventBus] ready for use.
func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[reflect.Type][]subscriber)}
}

// Subscribe registers fn as a handler for events of type T and returns an
// [Unsubscribe] function that removes it.
//
// It returns [ErrNilBus] if b is nil, [ErrNilSubscriber] if fn is nil and
// [ErrBusClosed] if the bus has been closed.
func Subscribe[T any](b *EventBus, fn func(ctx context.Context, ev T) error) (Unsubscribe, error) {
	if b == nil {
		return nil, ErrNilBus
	}
	if fn == nil {
		return nil, ErrNilSubscriber
	}

	t := reflect.TypeFor[T]()

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, ErrBusClosed
	}

	b.nextID++
	id := b.nextID
	b.subs[t] = append(b.subs[t], subscriber{
		id: id,
		fn: func(ctx context.Context, ev any) error {
			// A checked assertion, not ev.(T): asserting a nil interface panics
			// whatever T is, so publishing a nil event to a Subscribe[any] handler
			// would blow up inside the subscriber. The zero value of T is the
			// faithful translation of "no value" here.
			typed, ok := ev.(T)
			if !ok {
				var zero T
				typed = zero
			}

			return fn(ctx, typed)
		},
	})

	var once sync.Once
	return func() { once.Do(func() { b.unsubscribe(t, id) }) }, nil
}

// SubscribeModule registers fn as a handler for events of type T on the bus the
// module received through its [AppContext], and ties the subscription to the
// module's lifecycle: it is removed automatically when the module is destroyed,
// or when a failed [BaseAppModule.Init] rolls back.
//
// Prefer it to plain [Subscribe] from inside a module. Subscribe hands back an
// [Unsubscribe] that the caller has to store somewhere and remember to invoke;
// when that does not happen the handler outlives the module, so a module that is
// stopped and started again is subscribed twice and every event is delivered
// twice — growing by one delivery per restart, while the old handler keeps a
// live reference to the destroyed module.
//
// It is the [EventBus] counterpart of [Revoke], which does the same job for a
// [Registry] contract.
//
// It returns [ErrNilModule] if m is nil and [ErrNoAppContext] if no [AppContext]
// has been injected yet. A [Manager] injects it before starting its modules, so
// call this from a start hook rather than from a constructor.
func SubscribeModule[T any](m *BaseAppModule, fn func(ctx context.Context, ev T) error) error {
	if m == nil {
		return ErrNilModule
	}

	appCtx := m.AppContext()
	if appCtx == nil {
		return ErrNoAppContext
	}

	unsub, err := Subscribe(appCtx.Bus, fn)
	if err != nil {
		return err
	}

	m.AddCleanup(func(context.Context) error {
		unsub()
		return nil
	})

	return nil
}

// unsubscribe removes the subscriber with the given id from the type bucket.
func (b *EventBus) unsubscribe(t reflect.Type, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subs := b.subs[t]
	for i, s := range subs {
		if s.id == id {
			subs = slices.Delete(subs, i, i+1)
			if len(subs) == 0 {
				// Drop the bucket entirely; otherwise the map only ever grows,
				// keeping an empty slice per event type ever subscribed to.
				delete(b.subs, t)
			} else {
				b.subs[t] = subs
			}

			break
		}
	}
}

// Publish delivers ev to every subscriber registered for its type, in
// registration order, and joins their errors via [errors.Join].
//
// Delivery is synchronous and stops early if ctx is canceled. A subscriber that
// panics is recovered and reported as an error. Publishing a type with no
// subscribers is a no-op and returns nil. It returns [ErrNilBus] if b is nil and
// [ErrBusClosed] if the bus has been closed.
//
// When T is inferred as an interface — which happens whenever the event travels
// through an any, an error or a domain interface on its way here — the event is
// delivered by its dynamic type as well as by T. Keying by T alone would look up
// the interface bucket, deliver to nobody and still return nil, losing the event
// without a trace.
func Publish[T any](ctx context.Context, b *EventBus, ev T) error {
	if b == nil {
		return ErrNilBus
	}

	primary, secondary := publishKeys(ev)

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrBusClosed
	}
	subs := slices.Clone(b.subs[primary])
	if secondary != nil {
		subs = append(subs, b.subs[secondary]...)
	}
	b.mu.RUnlock()

	var errs []error
	for _, s := range subs {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := deliver(ctx, s.fn, ev); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// publishKeys returns the type keys an event must be delivered under: primary
// always, secondary only when non-nil.
//
// Events are keyed by their Go type, and for a concrete T that is exactly T.
// But reflect reports the *static* type of the parameter, so the moment a value
// reaches [Publish] through an interface variable T is inferred as that
// interface: `var ev any = UserCreated{}` keys the event as `any`, not as
// UserCreated. The dynamic type is therefore used as well, and it is the primary
// key so that concrete subscribers — the common case — are served first.
//
// Subscribers registered for the interface itself keep working: a subscriber
// belongs to exactly one key, so nothing is delivered twice.
//
// Two return values rather than a slice: Publish is the hot path, and the common
// case must not allocate just to carry a single key.
func publishKeys[T any](ev T) (primary, secondary reflect.Type) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Interface {
		return t, nil
	}

	dyn := reflect.TypeOf(ev)
	if dyn == nil {
		// A nil interface carries no dynamic type; only T can be keyed.
		return t, nil
	}

	return dyn, t
}

// deliver invokes a single subscriber, converting a panic into an error so a
// misbehaving subscriber cannot crash the publisher.
func deliver(ctx context.Context, fn func(context.Context, any) error, ev any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("appmod: event subscriber panicked: %v", r)
		}
	}()

	return fn(ctx, ev)
}

// Close removes all subscriptions and marks the bus closed. Subsequent
// [Subscribe]/[Publish] calls return [ErrBusClosed]. Close is idempotent.
//
// It returns nothing: closing a bus cannot fail, and an error return that is
// always nil only invites callers to write handling that never runs.
func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	clear(b.subs)
}
