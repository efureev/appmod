// Command manager demonstrates orchestrating a graph of modules with
// appmod.Manager.
//
// It shows how to:
//   - define a custom module by embedding appmod.BaseAppModule and adding a
//     HealthCheck method so it participates in Manager.Health;
//   - register modules with dependencies so the manager starts them in
//     topological order and stops them in reverse;
//   - start independent modules concurrently (the two leaf modules here);
//   - probe readiness with Manager.Health;
//   - run the whole application with Manager.Run, which blocks until the
//     context is canceled (or SIGINT/SIGTERM) and then gracefully shuts down
//     within the configured timeout.
//
// The dependency graph used below:
//
//	config        (no deps)
//	  ├── db      (depends on config)
//	  └── cache   (depends on config)   <- db and cache start concurrently
//	        api   (depends on db and cache)
//
// Run it with:
//
//	go run ./examples/manager
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/efureev/appmod/v2"
)

// service is a custom module: it embeds BaseAppModule for the lifecycle plumbing
// and adds a HealthCheck so the Manager can probe it.
type service struct {
	appmod.BaseAppModule
}

// newService builds a named service whose start/stop are traced to stdout.
func newService(name string) *service {
	s := &service{}
	s.SetConfig(appmod.NewConfig(name, "v1"))
	s.BeforeStart(func(_ context.Context, m appmod.HookModule) error {
		fmt.Printf("  start  %s\n", m.Name())
		return nil
	})
	s.BeforeDestroy(func(_ context.Context, m appmod.HookModule) error {
		fmt.Printf("  stop   %s\n", m.Name())
		return nil
	})

	return s
}

// HealthCheck makes *service satisfy appmod.HealthChecker.
func (s *service) HealthCheck(_ context.Context) error {
	if s.State() != appmod.StateRunning {
		return fmt.Errorf("module %q is not running (state=%s)", s.Name(), s.State())
	}

	return nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	mgr := appmod.NewManager(
		appmod.WithLogger(logger),
		appmod.WithShutdownTimeout(5*time.Second),
	)

	// Register the graph. Dependencies may be declared before or after the
	// modules they reference; they are validated on Start.
	must(mgr.Register("config", newService("config")))
	must(mgr.Register("db", newService("db"), "config"))
	must(mgr.Register("cache", newService("cache"), "config"))
	must(mgr.Register("api", newService("api"), "db", "cache"))

	fmt.Println("registered modules:", mgr.Modules())

	// Run blocks until the context is canceled or a termination signal arrives,
	// then stops every module in reverse order. Here we auto-cancel after a
	// short delay so the example terminates on its own; in a real application
	// you would pass context.Background() and rely on SIGINT/SIGTERM.
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		// Give Start time to finish, run a health probe, then trigger shutdown.
		time.Sleep(200 * time.Millisecond)
		if err := mgr.Health(ctx); err != nil {
			fmt.Println("health: UNHEALTHY:", err)
		} else {
			fmt.Println("health: all modules healthy")
		}
		fmt.Println("-- triggering graceful shutdown --")
		cancel()
	}()

	fmt.Println("-- starting application --")
	if err := mgr.Run(ctx); err != nil {
		fmt.Println("run error:", err)
		os.Exit(1)
	}
	fmt.Println("-- application stopped cleanly --")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
