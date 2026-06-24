package appmod

import (
	"context"
	"errors"
	"fmt"
)

// ExampleBaseAppModule demonstrates the basic lifecycle of a module: register
// hooks, initialize the module and tear it down.
func ExampleBaseAppModule() {
	mod := &BaseAppModule{}
	mod.SetConfig(NewConfig("My Module", "v1.0.0"))

	mod.BeforeStart(func(_ context.Context, m HookModule) error {
		fmt.Printf("starting %s %s\n", m.Config().Name(), m.Config().Version())
		return nil
	})
	mod.AfterStart(func(_ context.Context, _ HookModule) error {
		fmt.Println("started")
		return nil
	})
	mod.BeforeDestroy(func(_ context.Context, m HookModule) error {
		fmt.Printf("stopping %s\n", m.Config().Name())
		return nil
	})

	ctx := context.Background()
	if err := mod.Init(ctx); err != nil {
		fmt.Println("init failed:", err)
		return
	}
	if err := mod.Destroy(ctx); err != nil {
		fmt.Println("destroy failed:", err)
		return
	}

	// Output:
	// starting My Module v1.0.0
	// started
	// stopping My Module
}

// ExampleBaseAppModule_abort shows how returning an error from a BeforeStart
// hook aborts initialization.
func ExampleBaseAppModule_abort() {
	mod := &BaseAppModule{}

	mod.BeforeStart(func(_ context.Context, _ HookModule) error {
		return errors.New("config is invalid")
	})

	if err := mod.Init(context.Background()); err != nil {
		fmt.Println("init failed:", err)
		fmt.Println("initialized:", mod.Initialized())
	}

	// Output:
	// init failed: appmod: BeforeStart hook #0 failed: config is invalid
	// initialized: false
}

// ExampleManager demonstrates orchestrating several modules connected by
// dependencies: the manager starts them in dependency order and stops them in
// reverse.
func ExampleManager() {
	newModule := func(name string) *BaseAppModule {
		m := &BaseAppModule{}
		m.SetConfig(NewConfig(name, "v1"))
		m.BeforeStart(func(_ context.Context, mod HookModule) error {
			fmt.Println("start", mod.Config().Name())
			return nil
		})
		m.BeforeDestroy(func(_ context.Context, mod HookModule) error {
			fmt.Println("stop", mod.Config().Name())
			return nil
		})
		return m
	}

	mgr := NewManager()
	_ = mgr.Register("db", newModule("db"))
	_ = mgr.Register("cache", newModule("cache"), "db")
	_ = mgr.Register("api", newModule("api"), "cache")

	ctx := context.Background()
	if err := mgr.Start(ctx); err != nil {
		fmt.Println("start failed:", err)
		return
	}
	if err := mgr.Stop(ctx); err != nil {
		fmt.Println("stop failed:", err)
		return
	}

	// Output:
	// start db
	// start cache
	// start api
	// stop api
	// stop cache
	// stop db
}

// ExampleEventBus demonstrates fire-and-forget (push) communication: a module
// subscribes to a typed event and another publishes it.
func ExampleEventBus() {
	type UserCreated struct{ ID string }

	bus := NewEventBus()

	_, _ = Subscribe(bus, func(_ context.Context, e UserCreated) error {
		fmt.Println("user created:", e.ID)
		return nil
	})

	_ = Publish(context.Background(), bus, UserCreated{ID: "user:1"})

	// Output:
	// user created: user:1
}

// ExampleRegistry demonstrates request/response (pull) communication: one
// module provides a contract and another requires it.
func ExampleRegistry() {
	type Greeter interface{ Greet() string }

	reg := NewRegistry()

	// The provider registers an implementation of the Greeter contract.
	_ = Provide[Greeter](reg, greeterImpl{})

	// A consumer obtains the contract without knowing the concrete type.
	g, err := Require[Greeter](reg)
	if err != nil {
		fmt.Println("require failed:", err)
		return
	}
	fmt.Println(g.Greet())

	// Output:
	// hello
}

type greeterImpl struct{}

func (greeterImpl) Greet() string { return "hello" }
