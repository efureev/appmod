package appmod

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
)

// recordingModule is a test module that records start/stop ordering into a
// shared, mutex-guarded log.
type recordingModule struct {
	BaseAppModule

	name     string
	log      *eventLog
	startErr error
	stopErr  error
	health   error
}

func newRecordingModule(name string, log *eventLog) *recordingModule {
	m := &recordingModule{name: name, log: log}
	m.SetConfig(NewConfig(name, "v1"))
	m.BeforeStart(func(_ context.Context, _ HookModule) error {
		log.add("start:" + name)
		return m.startErr
	})
	m.BeforeDestroy(func(_ context.Context, _ HookModule) error {
		log.add("stop:" + name)
		return m.stopErr
	})

	return m
}

func (m *recordingModule) HealthCheck(_ context.Context) error { return m.health }

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.events = append(l.events, e)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.events)
}

func TestManagerRegister(t *testing.T) {
	t.Run("EmptyName", func(t *testing.T) {
		mgr := NewManager()
		if err := mgr.Register("", &BaseAppModule{}); !errors.Is(err, ErrEmptyName) {
			t.Errorf("Register() = %v, want %v", err, ErrEmptyName)
		}
	})

	t.Run("NilModule", func(t *testing.T) {
		mgr := NewManager()
		if err := mgr.Register("a", nil); !errors.Is(err, ErrNilModule) {
			t.Errorf("Register() = %v, want %v", err, ErrNilModule)
		}
	})

	t.Run("Duplicate", func(t *testing.T) {
		mgr := NewManager()
		if err := mgr.Register("a", &BaseAppModule{}); err != nil {
			t.Fatalf("Register() = %v, want nil", err)
		}
		if err := mgr.Register("a", &BaseAppModule{}); !errors.Is(err, ErrDuplicateModule) {
			t.Errorf("Register() = %v, want %v", err, ErrDuplicateModule)
		}
	})

	t.Run("Modules", func(t *testing.T) {
		mgr := NewManager()
		_ = mgr.Register("b", &BaseAppModule{})
		_ = mgr.Register("a", &BaseAppModule{})
		_ = mgr.Register("c", &BaseAppModule{})

		want := []string{"a", "b", "c"}
		if got := mgr.Modules(); !slices.Equal(got, want) {
			t.Errorf("Modules() = %v, want %v", got, want)
		}
	})
}

func TestManagerStartStopOrder(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()

	// db <- cache <- api ; logger is independent.
	mustRegister(t, mgr, "db", newRecordingModule("db", log))
	mustRegister(t, mgr, "cache", newRecordingModule("cache", log), "db")
	mustRegister(t, mgr, "api", newRecordingModule("api", log), "cache")
	mustRegister(t, mgr, "logger", newRecordingModule("logger", log))

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	events := log.snapshot()

	// db must start before cache before api.
	assertBefore(t, events, "start:db", "start:cache")
	assertBefore(t, events, "start:cache", "start:api")

	if err := mgr.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	events = log.snapshot()
	// Teardown is reverse: api before cache before db.
	assertBefore(t, events, "stop:api", "stop:cache")
	assertBefore(t, events, "stop:cache", "stop:db")
}

func TestManagerUnknownDependency(t *testing.T) {
	mgr := NewManager()
	mustRegister(t, mgr, "a", &BaseAppModule{}, "missing")

	if err := mgr.Start(t.Context()); !errors.Is(err, ErrUnknownDependency) {
		t.Errorf("Start() = %v, want %v", err, ErrUnknownDependency)
	}
}

func TestManagerDependencyCycle(t *testing.T) {
	mgr := NewManager()
	mustRegister(t, mgr, "a", &BaseAppModule{}, "b")
	mustRegister(t, mgr, "b", &BaseAppModule{}, "a")

	if err := mgr.Start(t.Context()); !errors.Is(err, ErrDependencyCycle) {
		t.Errorf("Start() = %v, want %v", err, ErrDependencyCycle)
	}
}

func TestManagerStartRollback(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()

	db := newRecordingModule("db", log)
	cache := newRecordingModule("cache", log)
	cache.startErr = errors.New("cache boom")

	mustRegister(t, mgr, "db", db)
	mustRegister(t, mgr, "cache", cache, "db")

	err := mgr.Start(t.Context())
	if err == nil {
		t.Fatal("Start() = nil, want error")
	}
	if !errors.Is(err, cache.startErr) {
		t.Errorf("Start() = %v, want to wrap %v", err, cache.startErr)
	}

	// db started successfully, so it must be rolled back (stopped).
	events := log.snapshot()
	if !slices.Contains(events, "stop:db") {
		t.Errorf("events = %v, want db to be stopped during rollback", events)
	}
}

func TestManagerParallelStart(t *testing.T) {
	mgr := NewManager()

	// Two independent modules that block until both have entered BeforeStart;
	// this only completes if they run concurrently.
	var wg sync.WaitGroup
	wg.Add(2)
	barrier := func(_ context.Context, _ HookModule) error {
		wg.Done()
		wg.Wait()
		return nil
	}

	a := &BaseAppModule{}
	a.BeforeStart(barrier)
	b := &BaseAppModule{}
	b.BeforeStart(barrier)

	mustRegister(t, mgr, "a", a)
	mustRegister(t, mgr, "b", b)

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
}

func TestManagerHealth(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()

	ok := newRecordingModule("ok", log)
	bad := newRecordingModule("bad", log)
	bad.health = errors.New("unhealthy")

	mustRegister(t, mgr, "ok", ok)
	mustRegister(t, mgr, "bad", bad)

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	if err := mgr.Health(t.Context()); !errors.Is(err, bad.health) {
		t.Errorf("Health() = %v, want to wrap %v", err, bad.health)
	}

	bad.health = nil
	if err := mgr.Health(t.Context()); err != nil {
		t.Errorf("Health() = %v, want nil after recovery", err)
	}
}

func mustRegister(t *testing.T, mgr *Manager, name string, mod AppModule, deps ...string) {
	t.Helper()
	if err := mgr.Register(name, mod, deps...); err != nil {
		t.Fatalf("Register(%q) = %v, want nil", name, err)
	}
}

func assertBefore(t *testing.T, events []string, first, second string) {
	t.Helper()
	i := slices.Index(events, first)
	j := slices.Index(events, second)
	if i < 0 {
		t.Fatalf("event %q not found in %v", first, events)
	}
	if j < 0 {
		t.Fatalf("event %q not found in %v", second, events)
	}
	if i >= j {
		t.Errorf("expected %q (at %d) before %q (at %d): %v", first, i, second, j, events)
	}
}
