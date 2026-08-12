package hubmod_test

import (
	"context"
	"fmt"

	"github.com/efureev/appmod/adapters/hubmod"
	"github.com/efureev/appmod/v4"
	"github.com/efureev/msghub/v3"
)

// Example wires a hub into an application: one module owns the bus, another
// takes it from the registry and subscribes with a lifecycle-scoped
// subscription, and a third publishes.
func Example() {
	ctx := context.Background()

	bus := msghub.New()
	mgr := appmod.NewManager()

	// The hub module owns the bus and tears it down last.
	must(mgr.Register("bus", hubmod.NewModule(bus)))
	// Everything using the bus declares a dependency on it, so the ordering
	// follows from the graph: subscribers start after the bus, and stop before
	// it goes down.
	must(mgr.Register("cache", newCacheModule(), "bus"))
	must(mgr.Register("api", newAPIModule(), "bus", "cache"))

	must(mgr.Start(ctx))

	// Let the queued notification reach the cache before shutting down.
	must(bus.Drain(ctx))
	must(mgr.Stop(ctx))

	// Output:
	// api: publishing UserCreated(user:1)
	// cache: invalidating user:1
}

type cacheModule struct {
	appmod.BaseAppModule
}

func newCacheModule() *cacheModule {
	m := &cacheModule{}
	m.SetConfig(appmod.NewConfig("cache", "v1"))

	m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
		bus, err := hubmod.Require(m.AppContext().Registry)
		if err != nil {
			return err
		}

		// SubscribeModule ties the subscription to this module: it is removed
		// on Destroy, so a restart does not subscribe a second handler.
		return hubmod.SubscribeModule(&m.BaseAppModule, bus, usersCreated,
			func(_ context.Context, ev userCreated) error {
				fmt.Println("cache: invalidating", ev.ID)

				return nil
			})
	})

	return m
}

type apiModule struct {
	appmod.BaseAppModule
}

func newAPIModule() *apiModule {
	m := &apiModule{}
	m.SetConfig(appmod.NewConfig("api", "v1"))

	m.AfterStart(func(ctx context.Context, _ appmod.HookModule) error {
		bus, err := hubmod.Require(m.AppContext().Registry)
		if err != nil {
			return err
		}

		fmt.Println("api: publishing UserCreated(user:1)")

		return msghub.Publish(ctx, bus, usersCreated, userCreated{ID: "user:1"})
	})

	return m
}

type userCreated struct{ ID string }

var usersCreated = msghub.NewTopic[userCreated]("user.created")

func must(err error) {
	if err != nil {
		panic(err)
	}
}
