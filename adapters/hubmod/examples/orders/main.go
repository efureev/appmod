// Command orders runs a small application on top of adapters/hubmod: four
// modules, one msghub bus, and a shutdown that tears the bus down last.
//
// It shows the three pieces of wiring the adapter provides, in the order an
// application meets them:
//
//   - hubmod.NewModule gives the bus a lifecycle. Every module that uses the bus
//     declares a dependency on it, so the graph decides the order: subscribers
//     start after the bus, and the bus is drained and closed after they stop.
//   - hubmod.Require takes the bus from the shared appmod.Registry in a start
//     hook, so no module depends on whoever created it.
//   - hubmod.SubscribeModule scopes a subscription to a module. The example
//     restarts the mailer mid-run to make the point: the next order reaches one
//     handler, not two.
//
// It also puts msghub's two delivery modes side by side on the same topic.
// inventory is Synchronous — its handler runs inside Publish and its error goes
// back to the publisher, because a shop must not confirm an order it cannot
// reserve stock for. mailer takes the default, a queue handled on its own
// goroutine, because sending mail must not hold up the checkout.
//
// The dependency graph used below:
//
//	bus                                  hubmod.NewModule -> Provide[*msghub.Hub]
//	  ├── inventory (bus)                Require + SubscribeModule, synchronous
//	  ├── mailer    (bus)                Require + SubscribeModule, queued
//	  └── shop      (bus, inventory, mailer)
//
// Run it from the adapter directory with:
//
//	go run ./examples/orders
package main

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/efureev/appmod/adapters/hubmod"
	"github.com/efureev/appmod/v4"
	"github.com/efureev/msghub/v3"
)

// --- The event ---------------------------------------------------------------
//
// A topic is the contract between publisher and subscribers: the payload type
// comes from the topic, so neither side has to agree on anything else.

type order struct {
	ID   string
	Item string
	Qty  int
}

var ordersPlaced = msghub.NewTopic[order]("order.placed")

// --- inventory module: reacts inline ------------------------------------------

type inventoryModule struct {
	appmod.BaseAppModule
}

func newInventory() *inventoryModule {
	m := &inventoryModule{}
	m.SetConfig(appmod.NewConfig("inventory", "v1"))

	// AfterStart, not the constructor: the Manager injects the AppContext before
	// it starts the modules, so the registry is only reachable from a hook.
	m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
		bus, err := hubmod.Require(m.AppContext().Registry)
		if err != nil {
			return err
		}

		// Synchronous: the handler runs inside Publish and returns its error to
		// the publisher. Reserving stock is a step the shop must not proceed
		// without.
		return hubmod.SubscribeModule(&m.BaseAppModule, bus, ordersPlaced,
			func(_ context.Context, o order) error {
				fmt.Printf("inventory: reserved %d x %s for %s\n", o.Qty, o.Item, o.ID)

				return nil
			},
			msghub.Synchronous())
	})

	return m
}

// --- mailer module: reacts on its own goroutine -------------------------------

type mailerModule struct {
	appmod.BaseAppModule

	sent atomic.Int64
}

func newMailer() *mailerModule {
	m := &mailerModule{}
	m.SetConfig(appmod.NewConfig("mailer", "v1"))

	m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
		bus, err := hubmod.Require(m.AppContext().Registry)
		if err != nil {
			return err
		}

		// Queued, the default: Publish hands the event to this subscriber's
		// queue and returns. Sending mail must not hold up the checkout, and a
		// slow mailer must not slow the inventory down either.
		return hubmod.SubscribeModule(&m.BaseAppModule, bus, ordersPlaced,
			func(_ context.Context, o order) error {
				fmt.Println("mailer: emailed confirmation for", o.ID)
				m.sent.Add(1)

				return nil
			})
	})

	// The tally is per run, so a restarted mailer starts counting again.
	m.BeforeDestroy(func(context.Context, appmod.HookModule) error {
		fmt.Printf("mailer: %d confirmation(s) sent\n", m.sent.Swap(0))

		return nil
	})

	return m
}

// --- shop module: publishes ---------------------------------------------------

type shopModule struct {
	appmod.BaseAppModule

	bus *msghub.Hub
}

func newShop() *shopModule {
	m := &shopModule{}
	m.SetConfig(appmod.NewConfig("shop", "v1"))

	m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
		bus, err := hubmod.Require(m.AppContext().Registry)
		if err != nil {
			return err
		}
		m.bus = bus

		return nil
	})

	return m
}

// PlaceOrder announces an order to whoever is listening. A real application
// would call this from a request handler; here main plays that part.
func (m *shopModule) PlaceOrder(ctx context.Context, o order) error {
	fmt.Printf("shop: %s placed (%d x %s)\n", o.ID, o.Qty, o.Item)

	return msghub.Publish(ctx, m.bus, ordersPlaced, o)
}

// --- the application ----------------------------------------------------------

func main() {
	ctx := context.Background()

	bus := msghub.New()
	mgr := appmod.NewManager()

	shop, mailer := newShop(), newMailer()

	must(mgr.Register("bus", hubmod.NewModule(bus)))
	must(mgr.Register("inventory", newInventory(), "bus"))
	must(mgr.Register("mailer", mailer, "bus"))
	must(mgr.Register("shop", shop, "bus", "inventory", "mailer"))

	fmt.Println("-- starting application --")
	must(mgr.Start(ctx))

	place(ctx, bus, shop, order{ID: "order-1", Item: "widget", Qty: 2})
	place(ctx, bus, shop, order{ID: "order-2", Item: "gizmo", Qty: 1})

	// The reason SubscribeModule exists. The subscription belongs to the module,
	// so Destroy removes it; a handle stored by hand would outlive the module and
	// the next order would be confirmed twice, once more per restart.
	fmt.Println("-- restarting the mailer --")
	must(mailer.Destroy(ctx))
	must(mailer.Init(ctx))

	place(ctx, bus, shop, order{ID: "order-3", Item: "gadget", Qty: 5})

	// Stop tears the modules down in reverse dependency order, so the bus module
	// goes last: by the time it drains and closes the hub, nothing can publish to
	// it any more.
	fmt.Println("-- stopping application --")
	must(mgr.Stop(ctx))
	fmt.Println("-- stopped --")
}

// place publishes an order and then waits for the queued subscribers to catch
// up. The wait is what makes this program print the same lines in the same order
// on every run: without it the mailer's goroutine races the next order.
func place(ctx context.Context, bus *msghub.Hub, shop *shopModule, o order) {
	must(shop.PlaceOrder(ctx, o))
	must(bus.Drain(ctx))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
