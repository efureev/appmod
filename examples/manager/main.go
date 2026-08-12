// Command manager demonstrates orchestrating a graph of modules with
// appmod.Manager and how modules reach one another at run time: a module
// exposes a contract with appmod.Provide and a dependent module obtains it with
// appmod.Require, both through the shared appmod.Registry. Here db provides DB,
// cache requires DB (and provides Cache) and api requires both Cache and DB.
//
// The worker module additionally shows shutdown observation: it watches
// m.AppContext().Done() and stops taking new work the moment the shutdown
// starts, rather than waiting for its own Destroy — which only runs after every
// module depending on it has already stopped.
//
// The Manager injects a single shared appmod.AppContext (Registry + Logger)
// into every module that embeds appmod.BaseAppModule, so a module can reach
// them through m.AppContext().
//
// Fire-and-forget notifications between modules are not part of this package;
// an event bus is one more thing a module publishes through the Registry. See
// adapters/hubmod for a worked example.
//
// The dependency graph used below:
//
//	config        (no deps)
//	  ├── db      (depends on config)        -> Provide[DB]
//	  ├── worker  (depends on config)        -> watches AppContext().Done()
//	  └── cache   (depends on config, db)    -> Require[DB], Provide[Cache]
//	        api   (depends on db and cache)  -> Require[Cache]+[DB]
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
	"sync"
	"time"

	"github.com/efureev/appmod/v4"
)

// --- Contracts shared between modules ---------------------------------------
//
// Modules depend on these interfaces, not on each other's concrete types.

// DB is the contract provided by the db module.
type DB interface {
	Query(ctx context.Context, key string) (string, error)
}

// Cache is the contract provided by the cache module.
type Cache interface {
	Get(ctx context.Context, key string) (string, bool)
}

// --- db module: provides DB --------------------------------------------------

type dbModule struct {
	appmod.BaseAppModule
	data map[string]string
}

func newDB() *dbModule {
	m := &dbModule{data: map[string]string{"user:1": "Alice"}}
	m.SetConfig(appmod.NewConfig("db", "v1"))

	m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
		fmt.Println("  db: providing DB contract")
		return appmod.Provide[DB](m.AppContext().Registry, m)
	})
	m.BeforeDestroy(func(_ context.Context, _ appmod.HookModule) error {
		// Revoke reports whether anything was removed, plus an error for a nil
		// registry — a wiring mistake worth surfacing rather than swallowing.
		_, err := appmod.Revoke[DB](m.AppContext().Registry)

		return err
	})

	return m
}

func (m *dbModule) Query(_ context.Context, key string) (string, error) {
	if v, ok := m.data[key]; ok {
		return v, nil
	}
	return "", fmt.Errorf("db: key %q not found", key)
}

// --- cache module: requires DB, provides Cache, subscribes to UserCreated ----

type cacheModule struct {
	appmod.BaseAppModule
	db    DB
	mu    sync.Mutex
	store map[string]string
}

func newCache() *cacheModule {
	m := &cacheModule{store: make(map[string]string)}
	m.SetConfig(appmod.NewConfig("cache", "v1"))

	m.AfterStart(func(ctx context.Context, _ appmod.HookModule) error {
		ac := m.AppContext()

		// pull: obtain the DB contract provided by the db module.
		db, err := appmod.Require[DB](ac.Registry)
		if err != nil {
			return err
		}
		m.db = db

		// warm the cache from the db.
		if v, err := db.Query(ctx, "user:1"); err == nil {
			m.set("user:1", v)
			fmt.Printf("  cache: warmed user:1 = %q from db\n", v)
		}

		fmt.Println("  cache: providing Cache contract")
		return appmod.Provide[Cache](ac.Registry, m)
	})

	return m
}

func (m *cacheModule) set(key, val string) {
	m.mu.Lock()
	m.store[key] = val
	m.mu.Unlock()
}

// Get returns a cached value, falling back to the db on a miss (and caching it).
func (m *cacheModule) Get(ctx context.Context, key string) (string, bool) {
	m.mu.Lock()
	v, ok := m.store[key]
	m.mu.Unlock()
	if ok {
		return v, true
	}
	if m.db != nil {
		if v, err := m.db.Query(ctx, key); err == nil {
			m.set(key, v)
			return v, true
		}
	}
	return "", false
}

// --- worker module: observes the shutdown without blocking -------------------

type workerModule struct {
	appmod.BaseAppModule
	done chan struct{}
}

func newWorker() *workerModule {
	m := &workerModule{done: make(chan struct{})}
	m.SetConfig(appmod.NewConfig("worker", "v1"))

	m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
		ticker := time.NewTicker(60 * time.Millisecond)

		go func() {
			defer close(m.done)
			defer ticker.Stop()

			for {
				select {
				case <-m.AppContext().Done():
					// The shutdown has begun. Stop taking new work now instead of
					// waiting for Destroy, which runs only after the modules that
					// depend on this one have stopped.
					fmt.Println("  worker: shutdown observed -> no new work")

					return
				case <-ticker.C:
					fmt.Println("  worker: processing a job")
				}
			}
		}()

		return nil
	})

	// Destroy waits for the loop to finish draining.
	m.BeforeDestroy(func(_ context.Context, _ appmod.HookModule) error {
		<-m.done
		fmt.Println("  worker: drained")

		return nil
	})

	return m
}

// --- api module: requires Cache and DB, publishes UserCreated ----------------

type apiModule struct {
	appmod.BaseAppModule
}

func newAPI() *apiModule {
	m := &apiModule{}
	m.SetConfig(appmod.NewConfig("api", "v1"))

	m.AfterStart(func(ctx context.Context, _ appmod.HookModule) error {
		ac := m.AppContext()

		// pull: obtain both contracts the api needs.
		cache, err := appmod.Require[Cache](ac.Registry)
		if err != nil {
			return err
		}
		if _, err := appmod.Require[DB](ac.Registry); err != nil {
			return err
		}

		if v, ok := cache.Get(ctx, "user:1"); ok {
			fmt.Printf("  api: read user:1 = %q (served from cache)\n", v)
		}
		if _, ok := cache.Get(ctx, "user:2"); !ok {
			fmt.Println("  api: read user:2 -> not found (cache miss + db miss)")
		}

		return nil
	})

	return m
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	mgr := appmod.NewManager(
		appmod.WithLogger(logger),
		appmod.WithShutdownTimeout(5*time.Second),
	)

	must(mgr.Register("config", appmod.New(appmod.WithConfig(appmod.NewConfig("config", "v1")))))
	must(mgr.Register("db", newDB(), "config"))
	must(mgr.Register("worker", newWorker(), "config"))
	must(mgr.Register("cache", newCache(), "config", "db"))
	must(mgr.Register("api", newAPI(), "db", "cache"))

	fmt.Println("registered modules:", mgr.Modules())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		fmt.Println("-- triggering graceful shutdown --")
		cancel()
	}()

	fmt.Println("-- starting application --")
	if err := mgr.Run(ctx); err != nil {
		fmt.Println("run error:", err)
	} else {
		fmt.Println("-- application stopped cleanly --")
	}

	// ExitCode reports 128+signum when a signal triggered the shutdown, and 0
	// otherwise, so a supervisor can tell a forced stop from a clean one.
	os.Exit(mgr.ExitCode())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
