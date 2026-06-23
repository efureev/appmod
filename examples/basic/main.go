// Command basic demonstrates the lifecycle of a single module built on top of
// appmod.BaseAppModule.
//
// It shows how to:
//   - give a module a configuration (name/version) via SetConfig;
//   - register the four lifecycle hooks (BeforeStart, AfterStart,
//     BeforeDestroy, AfterDestroy);
//   - observe the module state machine (Created → Running → Destroyed);
//   - access the configuration from inside a hook through the narrow
//     appmod.HookModule view.
//
// Run it with:
//
//	go run ./examples/basic
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/efureev/appmod/v2"
)

func main() {
	// A BaseAppModule is ready to use with its zero value; SetConfig gives it
	// a name and version.
	mod := &appmod.BaseAppModule{}
	mod.SetConfig(appmod.NewConfig("greeter", "v1.2.0"))

	fmt.Println("state after creation:", mod.State()) // Created

	// Hooks run in the order: BeforeStart → (Running) → AfterStart during Init,
	// and BeforeDestroy → (Destroyed) → AfterDestroy during Destroy.
	mod.BeforeStart(func(_ context.Context, m appmod.HookModule) error {
		fmt.Printf("[before-start] preparing %s %s\n", m.Config().Name(), m.Config().Version())
		return nil
	})
	mod.AfterStart(func(_ context.Context, m appmod.HookModule) error {
		fmt.Printf("[after-start] %s is now %s\n", m.Config().Name(), m.State())
		return nil
	})
	mod.BeforeDestroy(func(_ context.Context, m appmod.HookModule) error {
		fmt.Printf("[before-destroy] releasing resources of %s\n", m.Config().Name())
		return nil
	})
	mod.AfterDestroy(func(_ context.Context, m appmod.HookModule) error {
		fmt.Printf("[after-destroy] %s is now %s\n", m.Config().Name(), m.State())
		return nil
	})

	ctx := context.Background()

	if err := mod.Init(ctx); err != nil {
		log.Fatalf("init failed: %v", err)
	}
	fmt.Println("state after init:", mod.State(), "| initialized:", mod.Initialized())

	if err := mod.Destroy(ctx); err != nil {
		log.Fatalf("destroy failed: %v", err)
	}
	fmt.Println("state after destroy:", mod.State(), "| initialized:", mod.Initialized())
}
