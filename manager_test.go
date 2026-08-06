package appmod

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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

func TestManagerStopContextCanceled(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()

	mustRegister(t, mgr, "db", newRecordingModule("db", log))
	mustRegister(t, mgr, "cache", newRecordingModule("cache", log), "db")
	mustRegister(t, mgr, "api", newRecordingModule("api", log), "cache")

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	// A canceled context must abort the teardown before touching any module,
	// so Stop returns promptly instead of leaking in a detached goroutine.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := mgr.Stop(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Stop() = %v, want to wrap %v", err, context.Canceled)
	}
	if events := log.snapshot(); len(events) != 3 {
		// Only the 3 start events; no module should have been stopped.
		t.Errorf("events = %v, want no stop events", events)
	}

	// The not-yet-stopped modules are retained, so a fresh Stop finishes the job.
	if err := mgr.Stop(t.Context()); err != nil {
		t.Fatalf("second Stop() = %v, want nil", err)
	}
	events := log.snapshot()
	assertBefore(t, events, "stop:api", "stop:cache")
	assertBefore(t, events, "stop:cache", "stop:db")
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

// TestManagerStartIsNotReentrant covers the guard on Start. Without it a second
// Start failed on the already-initialized modules, treated that as a startup
// failure and rolled back — stopping the modules the first Start had brought up.
func TestManagerStartIsNotReentrant(t *testing.T) {
	t.Run("SecondStartLeavesModulesRunning", func(t *testing.T) {
		log := &eventLog{}
		mgr := NewManager()
		mod := newRecordingModule("a", log)
		mustRegister(t, mgr, "a", mod)

		if err := mgr.Start(t.Context()); err != nil {
			t.Fatalf("Start() = %v, want nil", err)
		}
		if err := mgr.Start(t.Context()); !errors.Is(err, ErrAlreadyStarted) {
			t.Errorf("second Start() = %v, want %v", err, ErrAlreadyStarted)
		}

		if got := mod.State(); got != StateRunning {
			t.Errorf("module state after refused Start = %v, want %v", got, StateRunning)
		}
		if events := log.snapshot(); slices.Contains(events, "stop:a") {
			t.Errorf("refused Start tore the module down: %v", events)
		}
	})

	t.Run("ConcurrentStart", func(t *testing.T) {
		log := &eventLog{}
		mgr := NewManager()
		mod := newRecordingModule("a", log)
		mustRegister(t, mgr, "a", mod)

		var (
			wg   sync.WaitGroup
			errs = make([]error, 2)
		)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = mgr.Start(context.Background())
			}()
		}
		wg.Wait()

		var okCount int
		for _, err := range errs {
			switch {
			case err == nil:
				okCount++
			case !errors.Is(err, ErrAlreadyStarted):
				t.Errorf("Start() = %v, want nil or %v", err, ErrAlreadyStarted)
			}
		}
		if okCount != 1 {
			t.Errorf("%d concurrent Start() calls succeeded, want exactly 1", okCount)
		}
		if got := mod.State(); got != StateRunning {
			t.Errorf("module state after concurrent Start = %v, want %v", got, StateRunning)
		}
	})

	t.Run("RestartAfterStop", func(t *testing.T) {
		log := &eventLog{}
		mgr := NewManager()
		mustRegister(t, mgr, "a", newRecordingModule("a", log))

		if err := mgr.Start(t.Context()); err != nil {
			t.Fatalf("Start() = %v, want nil", err)
		}
		if err := mgr.Stop(t.Context()); err != nil {
			t.Fatalf("Stop() = %v, want nil", err)
		}
		if err := mgr.Start(t.Context()); err != nil {
			t.Errorf("Start() after Stop = %v, want nil", err)
		}

		want := []string{"start:a", "stop:a", "start:a"}
		if got := log.snapshot(); !slices.Equal(got, want) {
			t.Errorf("event log = %v, want %v", got, want)
		}
	})
}

// TestManagerHealthConcurrentRegister pins the fix for a data race: Health used
// to copy the node map by reference and read it after releasing the mutex, which
// races a concurrent Register. Run under -race.
func TestManagerHealthConcurrentRegister(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()
	mustRegister(t, mgr, "a", newRecordingModule("a", log))

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 500 {
			_ = mgr.Register(fmt.Sprintf("m%d", i), newRecordingModule("m", log))
		}
	}()
	go func() {
		defer wg.Done()
		for range 500 {
			if err := mgr.Health(context.Background()); err != nil {
				t.Errorf("Health() = %v, want nil", err)
				return
			}
		}
	}()
	wg.Wait()
}

// blockingModule stalls its teardown so a serial Stop is distinguishable from a
// layered, concurrent one by wall time.
type blockingModule struct {
	BaseAppModule

	delay time.Duration
}

func newBlockingModule(name string, delay time.Duration) *blockingModule {
	m := &blockingModule{delay: delay}
	m.SetConfig(NewConfig(name, "v1"))
	m.BeforeDestroy(func(_ context.Context, _ HookModule) error {
		time.Sleep(delay)
		return nil
	})

	return m
}

// TestManagerStopIsLayered pins the fix for a serial teardown: Start brought a
// layer up concurrently while Stop walked m.started one module at a time, so
// shutdown cost the sum of every module's teardown instead of the maximum per
// layer — which is what overruns a WithShutdownTimeout budget.
func TestManagerStopIsLayered(t *testing.T) {
	const (
		modules = 5
		delay   = 40 * time.Millisecond
	)

	mgr := NewManager()
	for i := range modules {
		mustRegister(t, mgr, fmt.Sprintf("m%d", i), newBlockingModule("m", delay))
	}

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	start := time.Now()
	if err := mgr.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
	elapsed := time.Since(start)

	// All modules are independent, so they form a single layer: the teardown
	// should cost about one delay, not modules*delay. A generous bound keeps the
	// test meaningful without being flaky on a loaded machine.
	if limit := delay * modules / 2; elapsed > limit {
		t.Errorf("Stop() took %v for %d independent modules of %v each; want well under %v (teardown is serial)",
			elapsed, modules, delay, limit)
	}
}

// TestManagerStopRespectsLayerOrder verifies that concurrency inside a layer did
// not cost the dependency ordering between layers.
func TestManagerStopRespectsLayerOrder(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()

	// db <- cache <- api; two independent modules share a layer with db.
	mustRegister(t, mgr, "db", newRecordingModule("db", log))
	mustRegister(t, mgr, "cache", newRecordingModule("cache", log), "db")
	mustRegister(t, mgr, "api", newRecordingModule("api", log), "cache")
	mustRegister(t, mgr, "metrics", newRecordingModule("metrics", log))
	mustRegister(t, mgr, "logger", newRecordingModule("logger", log))

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	if err := mgr.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	events := log.snapshot()
	assertBefore(t, events, "stop:api", "stop:cache")
	assertBefore(t, events, "stop:cache", "stop:db")

	var stops int
	for _, e := range events {
		if strings.HasPrefix(e, "stop:") {
			stops++
		}
	}
	if stops != 5 {
		t.Errorf("got %d stop events, want 5 (%v)", stops, events)
	}
}

// TestManagerStopFallbackOnUnplannableGraph covers a module registered after
// Start that makes the graph unplannable: the teardown must still run, falling
// back to the reverse of the start order.
func TestManagerStopFallbackOnUnplannableGraph(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()

	mustRegister(t, mgr, "db", newRecordingModule("db", log))
	mustRegister(t, mgr, "api", newRecordingModule("api", log), "db")

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	// Registered after Start, depends on a name that does not exist: plan() now
	// fails, but the started modules must still be torn down.
	mustRegister(t, mgr, "late", newRecordingModule("late", log), "nope")

	if err := mgr.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	events := log.snapshot()
	assertBefore(t, events, "stop:api", "stop:db")
	if slices.Contains(events, "stop:late") {
		t.Errorf("a module that was never started was torn down: %v", events)
	}
}

// TestManagerRun covers the entry point applications actually call. It had no
// test at all, which is how a canceled context came to skip the teardown
// entirely: without a shutdown timeout the shutdown package passes Run's own
// context to the teardown, and its cancellation is precisely what woke Run.
func TestManagerRun(t *testing.T) {
	t.Run("ContextCancelStopsModules", func(t *testing.T) {
		log := &eventLog{}
		mgr := NewManager()
		mustRegister(t, mgr, "db", newRecordingModule("db", log))
		mustRegister(t, mgr, "api", newRecordingModule("api", log), "db")

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- mgr.Run(ctx) }()

		waitFor(t, func() bool { return slices.Contains(log.snapshot(), "start:api") })
		cancel()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run() = %v, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run() did not return after the context was canceled")
		}

		events := log.snapshot()
		if !slices.Contains(events, "stop:api") || !slices.Contains(events, "stop:db") {
			t.Errorf("Run() did not stop every module: %v", events)
		}
		assertBefore(t, events, "stop:api", "stop:db")
	})

	t.Run("ReturnsStartError", func(t *testing.T) {
		log := &eventLog{}
		mgr := NewManager()
		bad := newRecordingModule("bad", log)
		bad.startErr = errors.New("cannot start")
		mustRegister(t, mgr, "bad", bad)

		if err := mgr.Run(t.Context()); !errors.Is(err, bad.startErr) {
			t.Errorf("Run() = %v, want to wrap %v", err, bad.startErr)
		}
	})

	t.Run("ShutdownTimeout", func(t *testing.T) {
		const timeout = 50 * time.Millisecond

		mgr := NewManager(WithShutdownTimeout(timeout))
		slow := newBlockingModule("slow", 10*timeout)
		mustRegister(t, mgr, "slow", slow)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- mgr.Run(ctx) }()

		waitFor(t, func() bool { return slow.State() == StateRunning })
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, ErrShutdownTimeout) {
				t.Errorf("Run() = %v, want to wrap %v", err, ErrShutdownTimeout)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run() did not return after the shutdown timeout")
		}
	})
}

// TestManagerWithLogger covers the option: the manager must report lifecycle
// events through the logger it was given.
func TestManagerWithLogger(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&syncWriter{w: &buf, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log := &eventLog{}
	mgr := NewManager(WithLogger(logger))
	mustRegister(t, mgr, "db", newRecordingModule("db", log))

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	if err := mgr.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	mu.Lock()
	out := buf.String()
	mu.Unlock()

	for _, want := range []string{"starting module", "stopping module", "module=db"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q: %s", want, out)
		}
	}
}

// syncWriter guards a buffer written to from the manager's start/stop goroutines.
type syncWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.w.Write(p)
}

// TestManagerStartRefusesUnfinishedTeardown covers the second guard in
// beginStart: a teardown aborted by its context leaves modules running, and
// starting again on top of them would initialize an already-running module.
func TestManagerStartRefusesUnfinishedTeardown(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()
	mustRegister(t, mgr, "db", newRecordingModule("db", log))

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := mgr.Stop(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop(canceled) = %v, want to wrap %v", err, context.Canceled)
	}

	if err := mgr.Start(t.Context()); !errors.Is(err, ErrAlreadyStarted) {
		t.Errorf("Start() with an unfinished teardown = %v, want %v", err, ErrAlreadyStarted)
	}

	// Finishing the teardown makes the manager startable again.
	if err := mgr.Stop(t.Context()); err != nil {
		t.Fatalf("second Stop() = %v, want nil", err)
	}
	if err := mgr.Start(t.Context()); err != nil {
		t.Errorf("Start() after a completed teardown = %v, want nil", err)
	}
}

// waitFor spins until cond holds or the test times out. Used instead of a sleep
// so the timing-sensitive Run tests do not depend on a fixed delay.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}

// plainModule implements AppModule without embedding BaseAppModule, and
// deliberately implements neither ContextAware nor HealthChecker. It proves the
// contract is usable on its own — the separation the package advertises — and
// exercises the Manager's capability checks on their negative branch.
type plainModule struct {
	mu      sync.Mutex
	config  AppModuleConfig
	state   State
	started *eventLog
	name    string
}

func newPlainModule(name string, log *eventLog) *plainModule {
	return &plainModule{config: NewConfig(name, "v1"), started: log, name: name}
}

func (p *plainModule) Config() AppModuleConfig { p.mu.Lock(); defer p.mu.Unlock(); return p.config }
func (p *plainModule) SetConfig(c AppModuleConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = c
}
func (p *plainModule) Name() string { return p.name }
func (p *plainModule) State() State { p.mu.Lock(); defer p.mu.Unlock(); return p.state }

func (p *plainModule) Init(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == StateRunning {
		return ErrAlreadyInitialized
	}
	p.state = StateRunning
	p.started.add("start:" + p.name)

	return nil
}

func (p *plainModule) Destroy(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateRunning {
		return ErrNotInitialized
	}
	p.state = StateDestroyed
	p.started.add("stop:" + p.name)

	return nil
}

func (p *plainModule) BeforeStart(HookFunc)          {}
func (p *plainModule) AfterStart(HookFunc)           {}
func (p *plainModule) BeforeDestroy(HookFunc)        {}
func (p *plainModule) AfterDestroy(HookFunc)         {}
func (p *plainModule) AddHook(Phase, Hook)           {}
func (p *plainModule) RemoveHook(Phase, string) bool { return false }

// TestManagerPlainModule orchestrates a module that implements AppModule on its
// own. The Manager must neither require BaseAppModule nor assume the optional
// ContextAware / HealthChecker capabilities are present.
func TestManagerPlainModule(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()

	plain := newPlainModule("plain", log)
	mustRegister(t, mgr, "plain", plain)
	mustRegister(t, mgr, "based", newRecordingModule("based", log), "plain")

	if err := mgr.Start(t.Context()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	// Health must skip the module that does not implement HealthChecker.
	if err := mgr.Health(t.Context()); err != nil {
		t.Errorf("Health() = %v, want nil", err)
	}
	if err := mgr.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	events := log.snapshot()
	assertBefore(t, events, "start:plain", "start:based")
	assertBefore(t, events, "stop:based", "stop:plain")
}

// TestManagerStartRollbackJoinsTeardownError covers abort's error path: when the
// rollback itself fails, both the cause and the teardown error must survive.
func TestManagerStartRollbackJoinsTeardownError(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()

	db := newRecordingModule("db", log)
	db.stopErr = errors.New("db refuses to stop")
	cache := newRecordingModule("cache", log)
	cache.startErr = errors.New("cache boom")

	mustRegister(t, mgr, "db", db)
	mustRegister(t, mgr, "cache", cache, "db")

	err := mgr.Start(t.Context())
	if !errors.Is(err, cache.startErr) {
		t.Errorf("Start() = %v, want to wrap the start error %v", err, cache.startErr)
	}
	if !errors.Is(err, db.stopErr) {
		t.Errorf("Start() = %v, want to wrap the teardown error %v", err, db.stopErr)
	}
}

// TestManagerStartContextCanceled covers the cancellation check between layers:
// Start must abort and roll back whatever it managed to bring up.
func TestManagerStartContextCanceled(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()
	mustRegister(t, mgr, "db", newRecordingModule("db", log))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := mgr.Start(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Start(canceled) = %v, want to wrap %v", err, context.Canceled)
	}
	if events := log.snapshot(); len(events) != 0 {
		t.Errorf("events = %v, want no module to have been started", events)
	}
	// The manager is left stoppable and startable again.
	if err := mgr.Start(t.Context()); err != nil {
		t.Errorf("Start() after a canceled attempt = %v, want nil", err)
	}
}

// TestManagerRunSingleUse pins the guard on the single-use shutdown sequence.
// Without it a second Run would start the modules and return the first Run's
// cached result immediately, never stopping anything.
func TestManagerRunSingleUse(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()
	mustRegister(t, mgr, "db", newRecordingModule("db", log))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Run(ctx) }()

	waitFor(t, func() bool { return slices.Contains(log.snapshot(), "start:db") })
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}

	if err := mgr.Run(t.Context()); !errors.Is(err, ErrAlreadyRun) {
		t.Errorf("second Run() = %v, want %v", err, ErrAlreadyRun)
	}
	// The refused Run must not have restarted anything.
	want := []string{"start:db", "stop:db"}
	if got := log.snapshot(); !slices.Equal(got, want) {
		t.Errorf("event log = %v, want %v", got, want)
	}
}

// TestManagerRunTimeoutNamesModules covers describeTimeout: go-shutdown can only
// report the single hook it was given, so Run annotates the timeout with the
// modules Stop did not get to.
func TestManagerRunTimeoutNamesModules(t *testing.T) {
	const timeout = 50 * time.Millisecond

	mgr := NewManager(WithShutdownTimeout(timeout))
	mustRegister(t, mgr, "slow", newBlockingModule("slow", 20*timeout))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Run(ctx) }()

	waitFor(t, func() bool { return mgr.Modules() != nil && len(mgr.startedNames()) == 1 })
	cancel()

	err := <-done
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Run() = %v, want to wrap %v", err, ErrShutdownTimeout)
	}
	if !strings.Contains(err.Error(), "slow") {
		t.Errorf("Run() = %v, want the error to name the module that hung", err)
	}
	// ErrShutdownTimeout wraps context.DeadlineExceeded, per the shutdown package.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run() = %v, want to wrap %v", err, context.DeadlineExceeded)
	}
}

// TestManagerExitCode covers the exit code an application passes to os.Exit.
func TestManagerExitCode(t *testing.T) {
	mgr := NewManager()
	mustRegister(t, mgr, "db", newRecordingModule("db", &eventLog{}))

	if got := mgr.ExitCode(); got != 0 {
		t.Errorf("ExitCode() before Run = %d, want 0", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Run(ctx) }()

	waitFor(t, func() bool { return len(mgr.startedNames()) == 1 })
	cancel()
	<-done

	// A context-triggered shutdown is not a signal, so the process exits cleanly.
	if got := mgr.ExitCode(); got != 0 {
		t.Errorf("ExitCode() after a context shutdown = %d, want 0", got)
	}
}

// TestManagerShutdown covers the manual trigger: it must wake a blocked Run
// exactly as a signal would.
func TestManagerShutdown(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager()
	mustRegister(t, mgr, "db", newRecordingModule("db", log))

	done := make(chan error, 1)
	go func() { done <- mgr.Run(context.Background()) }()

	waitFor(t, func() bool { return slices.Contains(log.snapshot(), "start:db") })
	mgr.Shutdown()
	mgr.Shutdown() // idempotent

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown() did not wake Run")
	}
	if !slices.Contains(log.snapshot(), "stop:db") {
		t.Errorf("modules were not stopped: %v", log.snapshot())
	}
}

// TestAppContextShutdownObservation covers the capability v3 made possible: a
// module learns that the application is going down without waiting for its own
// Destroy, which only runs after everything depending on it has stopped.
func TestAppContextShutdownObservation(t *testing.T) {
	t.Run("ClosesOnRun", func(t *testing.T) {
		mgr := NewManager()

		observed := make(chan struct{})
		mod := New(WithConfig(NewConfig("worker", "v1")))
		mod.AfterStart(func(_ context.Context, _ HookModule) error {
			go func() {
				<-mod.AppContext().Done()
				close(observed)
			}()

			return nil
		})
		mustRegister(t, mgr, "worker", mod)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- mgr.Run(ctx) }()

		waitFor(t, func() bool { return mod.State() == StateRunning })
		select {
		case <-observed:
			t.Fatal("Done() closed before the shutdown started")
		default:
		}

		cancel()
		select {
		case <-observed:
		case <-time.After(5 * time.Second):
			t.Fatal("Done() never closed")
		}
		<-done
	})

	t.Run("ClosesOnDirectStop", func(t *testing.T) {
		mgr := NewManager()
		mod := New(WithConfig(NewConfig("worker", "v1")))
		mustRegister(t, mgr, "worker", mod)

		if err := mgr.Start(t.Context()); err != nil {
			t.Fatalf("Start() = %v, want nil", err)
		}
		select {
		case <-mod.AppContext().Done():
			t.Fatal("Done() closed while the application was running")
		default:
		}

		if err := mgr.Stop(t.Context()); err != nil {
			t.Fatalf("Stop() = %v, want nil", err)
		}
		select {
		case <-mod.AppContext().Done():
		default:
			t.Error("Done() did not close after a direct Stop")
		}

		// Context() carries the same signal, for work that takes a context.
		shutCtx := mod.AppContext().Context()
		if shutCtx == context.Background() {
			t.Fatal("Context() = context.Background(), want the shutdown context")
		}
		if err := shutCtx.Err(); err == nil {
			t.Error("Context() is not canceled after the shutdown started")
		}
	})

	t.Run("HandBuiltContextNeverShutsDown", func(t *testing.T) {
		// AppContext is a public struct; one assembled by hand has no shutdown to
		// observe and must not panic.
		ac := &AppContext{Bus: NewEventBus(), Registry: NewRegistry()}

		if ac.Done() != nil {
			t.Error("Done() = non-nil, want a nil channel that blocks forever")
		}
		if ac.Context() != context.Background() {
			t.Error("Context() = custom, want context.Background()")
		}

		var nilCtx *AppContext
		if nilCtx.Done() != nil || nilCtx.Context() != context.Background() {
			t.Error("a nil *AppContext must report no shutdown rather than panic")
		}
	})
}

// TestManagerSignalOptions covers the two options that reach the shutdown
// sequence. They are checked through behavior: a manager configured with them
// still starts, stops and observes shutdown normally.
func TestManagerSignalOptions(t *testing.T) {
	log := &eventLog{}
	mgr := NewManager(
		WithSignals(syscall.SIGUSR1),
		WithForceOnSecondSignal(false),
	)
	mustRegister(t, mgr, "db", newRecordingModule("db", log))

	done := make(chan error, 1)
	go func() { done <- mgr.Run(context.Background()) }()

	waitFor(t, func() bool { return slices.Contains(log.snapshot(), "start:db") })

	// SIGINT is no longer watched, so only the explicit trigger ends the run.
	mgr.Shutdown()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
	}
	if !slices.Contains(log.snapshot(), "stop:db") {
		t.Errorf("modules were not stopped: %v", log.snapshot())
	}
}

// TestDescribeTimeoutWithoutRemainingModules covers the branch where the budget
// ran out but the teardown had in fact finished: there is nothing to name, so
// the error is passed through unchanged.
func TestDescribeTimeoutWithoutRemainingModules(t *testing.T) {
	mgr := NewManager()

	if got := mgr.describeTimeout(nil); got != nil {
		t.Errorf("describeTimeout(nil) = %v, want nil", got)
	}

	other := errors.New("not a timeout")
	if got := mgr.describeTimeout(other); !errors.Is(got, other) {
		t.Errorf("describeTimeout(other) = %v, want it untouched", got)
	}

	// No module is started, so the timeout carries no module list.
	got := mgr.describeTimeout(ErrShutdownTimeout)
	if !errors.Is(got, ErrShutdownTimeout) {
		t.Fatalf("describeTimeout() = %v, want to wrap %v", got, ErrShutdownTimeout)
	}
	if strings.Contains(got.Error(), "still running") {
		t.Errorf("describeTimeout() = %v, want no module list when nothing is started", got)
	}
}
