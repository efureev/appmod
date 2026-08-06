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

// unsubscribe removes the subscriber with the given id from the type bucket.
func (b *EventBus) unsubscribe(t reflect.Type, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subs := b.subs[t]
	for i, s := range subs {
		if s.id == id {
			b.subs[t] = slices.Delete(subs, i, i+1)
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
// without a trace; see [publishKeys].
func Publish[T any](ctx context.Context, b *EventBus, ev T) error {
	if b == nil {
		return ErrNilBus
	}

	keys := publishKeys(ev)

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrBusClosed
	}
	var subs []subscriber
	for _, t := range keys {
		subs = append(subs, b.subs[t]...)
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

// publishKeys returns the type keys an event must be delivered under.
//
// Events are keyed by their Go type, and for a concrete T that is exactly T.
// But reflect reports the *static* type of the parameter, so the moment a value
// reaches [Publish] through an interface variable T is inferred as that
// interface: `var ev any = UserCreated{}` keys the event as `any`, not as
// UserCreated. The dynamic type is therefore used as well, and it comes first so
// that concrete subscribers — the common case — are served before any
// interface-typed ones.
//
// Subscribers registered for the interface itself keep working: a subscriber
// belongs to exactly one key, so nothing is delivered twice.
func publishKeys[T any](ev T) []reflect.Type {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Interface {
		return []reflect.Type{t}
	}

	dyn := reflect.TypeOf(ev)
	if dyn == nil {
		// A nil interface carries no dynamic type; only T can be keyed.
		return []reflect.Type{t}
	}

	return []reflect.Type{dyn, t}
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
func (b *EventBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	clear(b.subs)

	return nil
}
