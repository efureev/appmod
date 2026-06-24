package appmod

import (
	"fmt"
	"reflect"
	"sync"
)

// Registry is a small, dependency-free, type-safe service locator that lets
// modules expose capabilities to one another by contract.
//
// A module publishes an implementation of a contract interface T with the
// generic [Provide]; a dependent module obtains it with the generic [Require].
// The contract is keyed by the Go type T (typically an interface), so consumers
// depend on the contract, not on the concrete provider type.
//
// A Registry is safe for concurrent use by multiple goroutines.
//
// Use the registry for request/response data access between modules (for
// example, the api module calling the cache module, which calls the db module).
// For fire-and-forget notifications use the [EventBus] instead.
//
// Ordering note: a module that calls [Require] for a contract must also declare
// a [Manager] dependency on the providing module, so that the provider's
// [Provide] (done in its AfterStart hook) has run before the consumer starts.
type Registry struct {
	mu       sync.RWMutex
	services map[reflect.Type]any
}

// NewRegistry creates an empty [Registry] ready for use.
func NewRegistry() *Registry {
	return &Registry{services: make(map[reflect.Type]any)}
}

// Provide registers impl as the implementation of contract T.
//
// It returns [ErrNilRegistry] if r is nil and [ErrDuplicateProvider] if a
// contract of type T has already been provided.
func Provide[T any](r *Registry, impl T) error {
	if r == nil {
		return ErrNilRegistry
	}

	t := reflect.TypeFor[T]()

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.services[t]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateProvider, t)
	}
	r.services[t] = impl

	return nil
}

// Require returns the implementation previously registered for contract T.
//
// It returns the zero value of T together with [ErrNilRegistry] if r is nil and
// [ErrProviderNotFound] if no provider has been registered for T.
func Require[T any](r *Registry) (T, error) {
	var zero T
	if r == nil {
		return zero, ErrNilRegistry
	}

	t := reflect.TypeFor[T]()

	r.mu.RLock()
	defer r.mu.RUnlock()

	v, ok := r.services[t]
	if !ok {
		return zero, fmt.Errorf("%w: %s", ErrProviderNotFound, t)
	}

	return v.(T), nil
}

// Revoke removes the implementation previously registered for contract T and
// reports whether one was removed. It is typically called by a provider in its
// BeforeDestroy hook so a restart does not leave a stale implementation behind.
func Revoke[T any](r *Registry) bool {
	if r == nil {
		return false
	}

	t := reflect.TypeFor[T]()

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.services[t]; !ok {
		return false
	}
	delete(r.services, t)

	return true
}
