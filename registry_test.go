package appmod

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// contracts used by the registry tests.
type greeter interface{ Greet() string }
type counter interface{ Inc() int }

type enGreeter struct{}

func (enGreeter) Greet() string { return "hello" }

func TestRegistryProvideRequire(t *testing.T) {
	reg := NewRegistry()

	if err := Provide[greeter](reg, enGreeter{}); err != nil {
		t.Fatalf("Provide() = %v, want nil", err)
	}

	g, err := Require[greeter](reg)
	if err != nil {
		t.Fatalf("Require() = %v, want nil", err)
	}
	if got := g.Greet(); got != "hello" {
		t.Errorf("Greet() = %q, want %q", got, "hello")
	}
}

func TestRegistryRequireNotFound(t *testing.T) {
	reg := NewRegistry()

	_, err := Require[greeter](reg)
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Require() = %v, want ErrProviderNotFound", err)
	}
}

func TestRegistryDuplicateProvider(t *testing.T) {
	reg := NewRegistry()

	if err := Provide[greeter](reg, enGreeter{}); err != nil {
		t.Fatalf("first Provide() = %v, want nil", err)
	}
	if err := Provide[greeter](reg, enGreeter{}); !errors.Is(err, ErrDuplicateProvider) {
		t.Errorf("second Provide() = %v, want ErrDuplicateProvider", err)
	}
}

func TestRegistryTypeIsolation(t *testing.T) {
	reg := NewRegistry()

	if err := Provide[greeter](reg, enGreeter{}); err != nil {
		t.Fatalf("Provide(greeter) = %v", err)
	}
	// A different contract must not be found even though something is provided.
	if _, err := Require[counter](reg); !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Require(counter) = %v, want ErrProviderNotFound", err)
	}
}

func TestRegistryRevoke(t *testing.T) {
	reg := NewRegistry()
	_ = Provide[greeter](reg, enGreeter{})

	if !Revoke[greeter](reg) {
		t.Error("Revoke() = false, want true")
	}
	if Revoke[greeter](reg) {
		t.Error("second Revoke() = true, want false")
	}
	if _, err := Require[greeter](reg); !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Require() after Revoke = %v, want ErrProviderNotFound", err)
	}

	// After revoking, the same contract can be provided again.
	if err := Provide[greeter](reg, enGreeter{}); err != nil {
		t.Errorf("Provide() after Revoke = %v, want nil", err)
	}
}

func TestRegistryNil(t *testing.T) {
	if err := Provide[greeter](nil, enGreeter{}); !errors.Is(err, ErrNilRegistry) {
		t.Errorf("Provide(nil) = %v, want ErrNilRegistry", err)
	}
	if _, err := Require[greeter](nil); !errors.Is(err, ErrNilRegistry) {
		t.Errorf("Require(nil) = %v, want ErrNilRegistry", err)
	}
	if Revoke[greeter](nil) {
		t.Error("Revoke(nil) = true, want false")
	}
}

func TestRegistryConcurrent(t *testing.T) {
	reg := NewRegistry()
	_ = Provide[greeter](reg, enGreeter{})

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g, err := Require[greeter](reg); err == nil {
				_ = g.Greet()
			}
		}()
	}
	wg.Wait()
}

// TestManagerInjectsContext verifies that the Manager injects a shared
// AppContext into ContextAware modules, so a consumer can Require a contract a
// dependency provided in its AfterStart.
func TestManagerInjectsContext(t *testing.T) {
	mgr := NewManager()

	provider := New(WithConfig(NewConfig("provider", "v1")))
	provider.AfterStart(func(_ context.Context, _ HookModule) error {
		return Provide[greeter](mgr.Registry(), enGreeter{})
	})

	var consumed string
	consumer := New(WithConfig(NewConfig("consumer", "v1")))
	consumer.AfterStart(func(_ context.Context, _ HookModule) error {
		g, err := Require[greeter](consumer.AppContext().Registry)
		if err != nil {
			return err
		}
		consumed = g.Greet()
		return nil
	})

	if err := mgr.Register("provider", provider); err != nil {
		t.Fatalf("Register(provider) = %v", err)
	}
	if err := mgr.Register("consumer", consumer, "provider"); err != nil {
		t.Fatalf("Register(consumer) = %v", err)
	}

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	t.Cleanup(func() { _ = mgr.Stop(context.WithoutCancel(t.Context())) })

	if consumed != "hello" {
		t.Errorf("consumer obtained %q via Require, want %q", consumed, "hello")
	}
}
