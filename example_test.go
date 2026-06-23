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

	mod.BeforeStart(func(_ context.Context, m AppModule) error {
		fmt.Printf("starting %s %s\n", m.Config().Name(), m.Config().Version())
		return nil
	})
	mod.AfterStart(func(_ context.Context, _ AppModule) error {
		fmt.Println("started")
		return nil
	})
	mod.BeforeDestroy(func(_ context.Context, m AppModule) error {
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

	mod.BeforeStart(func(_ context.Context, _ AppModule) error {
		return errors.New("config is invalid")
	})

	if err := mod.Init(context.Background()); err != nil {
		fmt.Println("init failed:", err)
		fmt.Println("initialized:", mod.Initialized())
	}

	// Output:
	// init failed: appmod: BeforeStart hook failed: config is invalid
	// initialized: false
}
