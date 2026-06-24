// Command manager demonstrates orchestrating a graph of modules with
// appmod.Manager and the two ways modules communicate at run time:
//
//   - pull (request/response) via the shared appmod.Registry: a module exposes
//     a contract with appmod.Provide and a dependent module obtains it with
//     appmod.Require. Here db provides DB, cache requires DB (and provides
//     Cache) and api requires both Cache and DB.
//   - push (fire-and-forget) via the shared appmod.EventBus: api publishes a
//     UserCreated event and cache, which subscribed to it during its start,
//     reacts by invalidating its entry.
//
// The Manager injects a single shared appmod.AppContext (EventBus + Registry +
// Logger) into every module that embeds appmod.BaseAppModule, so a module can
// reach them through m.AppContext().
//
// The dependency graph used below:
//
//	config        (no deps)
//	  ├── db      (depends on config)        -> Provide[DB]
//	  └── cache   (depends on config, db)    -> Require[DB], Provide[Cache], Subscribe[UserCreated]
//	        api   (depends on db and cache)  -> Require[Cache]+[DB], Publish[UserCreated]
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

	"github.com/efureev/appmod/v2"
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

// UserCreated is a fire-and-forget event published on the EventBus.
type UserCreated struct{ ID string }

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
		appmod.Revoke[DB](m.AppContext().Registry)
		return nil
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

		// push: react to UserCreated events published anywhere in the app.
		if _, err := appmod.Subscribe(ac.Bus, func(_ context.Context, e UserCreated) error {
			fmt.Printf("  cache: UserCreated(%s) received -> invalidating entry\n", e.ID)
			m.mu.Lock()
			delete(m.store, e.ID)
			m.mu.Unlock()
			return nil
		}); err != nil {
			return err
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

		// push: notify the rest of the app; the cache reacts to this.
		fmt.Println("  api: publishing UserCreated(user:1)")
		return appmod.Publish(ctx, ac.Bus, UserCreated{ID: "user:1"})
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
		os.Exit(1)
	}
	fmt.Println("-- application stopped cleanly --")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
