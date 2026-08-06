package appmod

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAppModuleConfig(t *testing.T) {
	config := Config{name: `test name`, version: `v1`}

	t.Run("Name", func(t *testing.T) {
		if got := config.Name(); got != "test name" {
			t.Errorf("Name() = %q, want %q", got, "test name")
		}
	})

	t.Run("Version", func(t *testing.T) {
		if got := config.Version(); got != "v1" {
			t.Errorf("Version() = %q, want %q", got, "v1")
		}
	})

	t.Run("Default", func(t *testing.T) {
		c := DefaultConfig()

		if got := c.Name(); got != "App Module" {
			t.Errorf("Name() = %q, want %q", got, "App Module")
		}
		if got := c.Version(); got != "v0.0.1" {
			t.Errorf("Version() = %q, want %q", got, "v0.0.1")
		}
	})

	t.Run("NewConfig", func(t *testing.T) {
		c := NewConfig("custom", "v9")

		if got := c.Name(); got != "custom" {
			t.Errorf("Name() = %q, want %q", got, "custom")
		}
		if got := c.Version(); got != "v9" {
			t.Errorf("Version() = %q, want %q", got, "v9")
		}
	})
}

func TestAppModuleBaseAppMod(t *testing.T) {
	t.Run("Config", func(t *testing.T) {
		mod := &BaseAppModule{config: Config{name: `test app`, version: `v1`}}

		if got := mod.Config().Name(); got != "test app" {
			t.Errorf("Config().Name() = %q, want %q", got, "test app")
		}
		if got := mod.Config().Version(); got != "v1" {
			t.Errorf("Config().Version() = %q, want %q", got, "v1")
		}

		mod.SetConfig(Config{name: `new test app`, version: `v2`})

		if got := mod.Config().Name(); got != "new test app" {
			t.Errorf("Config().Name() = %q, want %q", got, "new test app")
		}
		if got := mod.Config().Version(); got != "v2" {
			t.Errorf("Config().Version() = %q, want %q", got, "v2")
		}
	})

	t.Run("Init", func(t *testing.T) {
		mod := &BaseAppModule{}
		if err := mod.Init(t.Context()); err != nil {
			t.Errorf("Init() = %v, want nil", err)
		}
		if got := mod.State(); got != StateRunning {
			t.Errorf("State() = %v, want %v after Init", got, StateRunning)
		}
	})

	t.Run("Init/AlreadyInitialized", func(t *testing.T) {
		mod := &BaseAppModule{}
		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		if err := mod.Init(t.Context()); !errors.Is(err, ErrAlreadyInitialized) {
			t.Errorf("second Init() = %v, want %v", err, ErrAlreadyInitialized)
		}
	})

	t.Run("Destroy/NotInitialized", func(t *testing.T) {
		mod := &BaseAppModule{}
		if err := mod.Destroy(t.Context()); !errors.Is(err, ErrNotInitialized) {
			t.Errorf("Destroy() = %v, want %v", err, ErrNotInitialized)
		}
	})

	t.Run("Destroy", func(t *testing.T) {
		mod := &BaseAppModule{}
		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		if err := mod.Destroy(t.Context()); err != nil {
			t.Errorf("Destroy() = %v, want nil", err)
		}
		if got := mod.State(); got == StateRunning {
			t.Error("State() = Running, want anything else after Destroy")
		}
	})

	t.Run("Events/NormalFly", func(t *testing.T) {
		mod := &BaseAppModule{config: Config{name: `test app`, version: `v1`}}

		var order []string

		newConfig := Config{name: `New Application`, version: `v3`}
		mod.BeforeStart(func(_ context.Context, m HookModule) error {
			order = append(order, "beforeStart")
			m.SetConfig(newConfig)
			return nil
		})
		mod.AfterStart(func(_ context.Context, _ HookModule) error {
			order = append(order, "afterStart")
			return nil
		})

		finishConfig := Config{name: `New Application 2`, version: `v4`}
		mod.BeforeDestroy(func(_ context.Context, m HookModule) error {
			order = append(order, "beforeDestroy")
			m.SetConfig(finishConfig)
			return nil
		})
		mod.AfterDestroy(func(_ context.Context, _ HookModule) error {
			order = append(order, "afterDestroy")
			return nil
		})

		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}

		if got := mod.Config().Name(); got != newConfig.Name() {
			t.Errorf("after Init Config().Name() = %q, want %q", got, newConfig.Name())
		}
		if got := mod.Config().Version(); got != newConfig.Version() {
			t.Errorf("after Init Config().Version() = %q, want %q", got, newConfig.Version())
		}

		if err := mod.Destroy(t.Context()); err != nil {
			t.Fatalf("Destroy() = %v, want nil", err)
		}

		if got := mod.Config().Name(); got != finishConfig.Name() {
			t.Errorf("after Destroy Config().Name() = %q, want %q", got, finishConfig.Name())
		}
		if got := mod.Config().Version(); got != finishConfig.Version() {
			t.Errorf("after Destroy Config().Version() = %q, want %q", got, finishConfig.Version())
		}

		want := []string{"beforeStart", "afterStart", "beforeDestroy", "afterDestroy"}
		if len(order) != len(want) {
			t.Fatalf("hook order = %v, want %v", order, want)
		}
		for i := range want {
			if order[i] != want[i] {
				t.Fatalf("hook order = %v, want %v", order, want)
			}
		}
	})

	t.Run("Events/MultipleHooks", func(t *testing.T) {
		mod := &BaseAppModule{}

		var calls int
		for range 3 {
			mod.BeforeStart(func(_ context.Context, _ HookModule) error {
				calls++
				return nil
			})
		}

		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		if calls != 3 {
			t.Errorf("BeforeStart hooks called %d times, want 3", calls)
		}
	})

	t.Run("Events/HookError", func(t *testing.T) {
		mod := &BaseAppModule{config: Config{name: `test app`, version: `v1`}}

		error1 := errors.New(`error BeforeStart`)
		error2 := errors.New(`error BeforeDestroy`)

		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			return error1
		})
		mod.BeforeDestroy(func(_ context.Context, _ HookModule) error {
			return error2
		})

		if err := mod.Init(t.Context()); !errors.Is(err, error1) {
			t.Errorf("Init() = %v, want %v", err, error1)
		}
		// BeforeStart failed, so the module stays not initialized.
		if got := mod.State(); got == StateRunning {
			t.Error("State() = Running, want anything else after failed Init")
		}

		// Force the module into the running state to exercise Destroy hook.
		mod.setState(StateRunning)
		if err := mod.Destroy(t.Context()); !errors.Is(err, error2) {
			t.Errorf("Destroy() = %v, want %v", err, error2)
		}
	})

	t.Run("Events/HookPanic", func(t *testing.T) {
		mod := &BaseAppModule{}
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			panic("boom")
		})

		err := mod.Init(t.Context())
		if err == nil {
			t.Fatal("Init() = nil, want error after panicking hook")
		}
		if got := mod.State(); got == StateRunning {
			t.Error("State() = Running, want anything else after panicking hook")
		}
	})

	t.Run("New/Options", func(t *testing.T) {
		var started bool
		mod := New(
			WithConfig(NewConfig("opt", "v1")),
			WithBeforeStart(func(_ context.Context, _ HookModule) error {
				started = true
				return nil
			}),
		)

		if got := mod.Config().Name(); got != "opt" {
			t.Errorf("Config().Name() = %q, want %q", got, "opt")
		}
		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		if !started {
			t.Error("BeforeStart hook from option was not executed")
		}
	})

	t.Run("Concurrent/InitDestroy", func(t *testing.T) {
		mod := &BaseAppModule{}

		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mod.BeforeStart(func(_ context.Context, _ HookModule) error { return nil })
				_ = mod.Init(t.Context())
				_ = mod.State()
				mod.SetConfig(NewConfig("c", "v"))
				_ = mod.Destroy(t.Context())
			}()
		}
		wg.Wait()
	})
}

func TestAppModuleLifecycleState(t *testing.T) {
	t.Run("States", func(t *testing.T) {
		mod := &BaseAppModule{}
		if got := mod.State(); got != StateCreated {
			t.Errorf("State() = %v, want %v", got, StateCreated)
		}

		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		if got := mod.State(); got != StateRunning {
			t.Errorf("State() = %v, want %v", got, StateRunning)
		}

		if err := mod.Destroy(t.Context()); err != nil {
			t.Fatalf("Destroy() = %v, want nil", err)
		}
		if got := mod.State(); got != StateDestroyed {
			t.Errorf("State() = %v, want %v", got, StateDestroyed)
		}

		// A destroyed module can be re-initialized.
		if err := mod.Init(t.Context()); err != nil {
			t.Errorf("re-Init() = %v, want nil", err)
		}
	})

	t.Run("String", func(t *testing.T) {
		cases := map[State]string{
			StateCreated:      "Created",
			StateInitializing: "Initializing",
			StateRunning:      "Running",
			StateDestroying:   "Destroying",
			StateDestroyed:    "Destroyed",
			StateFailed:       "Failed",
			State(42):         "State(42)",
		}
		for s, want := range cases {
			if got := s.String(); got != want {
				t.Errorf("State(%d).String() = %q, want %q", int32(s), got, want)
			}
		}
	})
}

func TestAppModuleInitRollback(t *testing.T) {
	t.Run("UnwindsCleanupsNotTeardownHooks", func(t *testing.T) {
		mod := &BaseAppModule{}

		var order []string
		// Two start hooks acquire something and register its release; the third
		// fails, so only the first two compensations are owed.
		for _, name := range []string{"one", "two"} {
			mod.BeforeStart(func(_ context.Context, _ HookModule) error {
				order = append(order, "acquire:"+name)
				mod.AddCleanup(func(_ context.Context) error {
					order = append(order, "release:"+name)
					return nil
				})

				return nil
			})
		}
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			order = append(order, "boom")
			return errors.New("boom")
		})
		// Teardown hooks describe stopping a started module; a failed start must
		// not invoke them, or they would undo work that never happened.
		mod.BeforeDestroy(func(_ context.Context, _ HookModule) error {
			order = append(order, "beforeDestroy")
			return nil
		})
		mod.AfterDestroy(func(_ context.Context, _ HookModule) error {
			order = append(order, "afterDestroy")
			return nil
		})

		if err := mod.Init(t.Context()); err == nil {
			t.Fatal("Init() = nil, want error")
		}
		if got := mod.State(); got != StateFailed {
			t.Errorf("State() = %v, want %v", got, StateFailed)
		}

		want := []string{"acquire:one", "acquire:two", "boom", "release:two", "release:one"}
		if !slices.Equal(order, want) {
			t.Errorf("order = %v, want %v", order, want)
		}
	})

	t.Run("AfterStartFailureRollsBack", func(t *testing.T) {
		mod := &BaseAppModule{}

		var released bool
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			mod.AddCleanup(func(_ context.Context) error {
				released = true
				return nil
			})

			return nil
		})
		mod.AfterStart(func(_ context.Context, _ HookModule) error {
			return errors.New("after start boom")
		})

		if err := mod.Init(t.Context()); err == nil {
			t.Fatal("Init() = nil, want error")
		}
		if got := mod.State(); got != StateFailed {
			t.Errorf("State() = %v, want %v", got, StateFailed)
		}
		if !released {
			t.Error("cleanup registered by BeforeStart was not run during rollback")
		}
	})

	t.Run("RollbackErrorIsJoined", func(t *testing.T) {
		mod := &BaseAppModule{}

		startErr := errors.New("start boom")
		rollbackErr := errors.New("cleanup boom")
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			mod.AddCleanup(func(_ context.Context) error { return rollbackErr })
			return nil
		})
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			return startErr
		})

		err := mod.Init(t.Context())
		if !errors.Is(err, startErr) {
			t.Errorf("Init() = %v, want to wrap %v", err, startErr)
		}
		if !errors.Is(err, rollbackErr) {
			t.Errorf("Init() = %v, want to wrap %v", err, rollbackErr)
		}
	})
}

func TestAppModuleContext(t *testing.T) {
	t.Run("CancelledContextAbortsInit", func(t *testing.T) {
		mod := &BaseAppModule{}

		var called bool
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			called = true
			return nil
		})

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := mod.Init(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Init() = %v, want %v", err, context.Canceled)
		}
		if called {
			t.Error("BeforeStart hook ran despite canceled context")
		}
		if got := mod.State(); got != StateFailed {
			t.Errorf("State() = %v, want %v", got, StateFailed)
		}
	})

	t.Run("CancellationBetweenHooks", func(t *testing.T) {
		mod := &BaseAppModule{}

		ctx, cancel := context.WithCancel(t.Context())
		var second bool
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			cancel()
			return nil
		})
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			second = true
			return nil
		})

		if err := mod.Init(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("Init() = %v, want %v", err, context.Canceled)
		}
		if second {
			t.Error("second BeforeStart hook ran after context cancellation")
		}
	})
}

// TestAppModuleAfterStartWindow covers the window between the BeforeStart and
// AfterStart phases. The module used to publish StateRunning before running the
// AfterStart hooks, which let a concurrent Destroy pass its state guard and tear
// the module down while Init was still starting it — running the teardown hooks
// twice and racing the two final setState calls.
func TestAppModuleAfterStartWindow(t *testing.T) {
	t.Run("NotRunningUntilInitReturns", func(t *testing.T) {
		var seen State
		mod := New(
			WithConfig(NewConfig("m", "v1")),
			WithAfterStart(func(_ context.Context, m HookModule) error {
				seen = m.State()
				return nil
			}),
		)

		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		if seen != StateInitializing {
			t.Errorf("state during AfterStart = %v, want %v", seen, StateInitializing)
		}
		if got := mod.State(); got != StateRunning {
			t.Errorf("State() after Init = %v, want %v", got, StateRunning)
		}
	})

	t.Run("ConcurrentDestroyDoesNotDoubleTeardown", func(t *testing.T) {
		var (
			teardowns atomic.Int32
			entered   = make(chan struct{})
			release   = make(chan struct{})
		)
		mod := New(WithConfig(NewConfig("m", "v1")))
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			mod.AddCleanup(func(_ context.Context) error {
				teardowns.Add(1)
				return nil
			})

			return nil
		})
		mod.AfterStart(func(_ context.Context, _ HookModule) error {
			close(entered)
			<-release

			return errors.New("afterstart failed")
		})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mod.Init(context.Background())
		}()

		<-entered
		if err := mod.Destroy(t.Context()); !errors.Is(err, ErrNotInitialized) {
			t.Errorf("Destroy() while Init is running = %v, want %v", err, ErrNotInitialized)
		}
		close(release)
		wg.Wait()

		if got := teardowns.Load(); got != 1 {
			t.Errorf("compensation ran %d time(s), want exactly 1 (rollback only)", got)
		}
		if got := mod.State(); got != StateFailed {
			t.Errorf("State() = %v, want %v", got, StateFailed)
		}
	})
}

// TestTeardownStateIsConsistent covers the state a teardown hook observes. It
// used to differ by path: StateInitializing when reached through Init's
// rollback, StateDestroying when reached through Destroy, so a hook branching on
// m.State() behaved differently depending on how it was called.
func TestTeardownStateIsConsistent(t *testing.T) {
	newMod := func(seen *State) AppModule {
		return New(
			WithConfig(NewConfig("m", "v1")),
			WithBeforeDestroy(func(_ context.Context, m HookModule) error {
				*seen = m.State()
				return nil
			}),
		)
	}

	var viaRollback State
	rollbackMod := New(WithConfig(NewConfig("m", "v1")))
	rollbackMod.BeforeStart(func(_ context.Context, _ HookModule) error {
		rollbackMod.AddCleanup(func(_ context.Context) error {
			viaRollback = rollbackMod.State()
			return nil
		})

		return nil
	})
	rollbackMod.AfterStart(func(_ context.Context, _ HookModule) error {
		return errors.New("boom")
	})
	_ = rollbackMod.Init(t.Context())

	var viaDestroy State
	ok := newMod(&viaDestroy)
	if err := ok.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}
	if err := ok.Destroy(t.Context()); err != nil {
		t.Fatalf("Destroy() = %v, want nil", err)
	}

	if viaRollback != StateDestroying {
		t.Errorf("state during rollback = %v, want %v", viaRollback, StateDestroying)
	}
	if viaRollback != viaDestroy {
		t.Errorf("teardown sees %v via rollback but %v via Destroy; want the same", viaRollback, viaDestroy)
	}
}

// TestZeroValueModuleHasDefaultConfig covers the "zero value is ready to use"
// claim: Config() used to return nil, so the package's own idiom
// Config().Name() inside a hook was a nil dereference.
func TestZeroValueModuleHasDefaultConfig(t *testing.T) {
	mod := &BaseAppModule{}

	if got := mod.Config(); got == nil {
		t.Fatal("Config() = nil, want DefaultConfig()")
	}
	if got, want := mod.Config().Name(), DefaultConfig().Name(); got != want {
		t.Errorf("Config().Name() = %q, want %q", got, want)
	}
	if got, want := mod.Name(), mod.Config().Name(); got != want {
		t.Errorf("Name() = %q, but Config().Name() = %q; they must agree", got, want)
	}

	// The idiom that used to panic must now work end to end.
	var seen string
	mod.BeforeStart(func(_ context.Context, m HookModule) error {
		seen = m.Config().Name()
		return nil
	})
	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}
	if seen != DefaultConfig().Name() {
		t.Errorf("hook saw Config().Name() = %q, want %q", seen, DefaultConfig().Name())
	}
}

// TestAddCleanup covers the cleanup registry backing SubscribeModule: LIFO
// order, execution on both teardown paths, and single execution.
func TestAddCleanup(t *testing.T) {
	t.Run("RunsLIFOOnDestroy", func(t *testing.T) {
		var order []string
		mod := New(WithConfig(NewConfig("m", "v1")))
		for _, name := range []string{"first", "second", "third"} {
			mod.AddCleanup(func(_ context.Context) error {
				order = append(order, name)
				return nil
			})
		}

		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		if err := mod.Destroy(t.Context()); err != nil {
			t.Fatalf("Destroy() = %v, want nil", err)
		}

		want := []string{"third", "second", "first"}
		if !slices.Equal(order, want) {
			t.Errorf("cleanup order = %v, want %v", order, want)
		}
	})

	t.Run("RunsOnRollback", func(t *testing.T) {
		var ran int
		mod := New(
			WithConfig(NewConfig("m", "v1")),
			WithAfterStart(func(_ context.Context, _ HookModule) error {
				return errors.New("boom")
			}),
		)
		mod.AddCleanup(func(_ context.Context) error {
			ran++
			return nil
		})

		if err := mod.Init(t.Context()); err == nil {
			t.Fatal("Init() = nil, want error")
		}
		if ran != 1 {
			t.Errorf("cleanup ran %d time(s) during rollback, want 1", ran)
		}
	})

	t.Run("RunsAtMostOnce", func(t *testing.T) {
		var ran int
		mod := New(WithConfig(NewConfig("m", "v1")))
		mod.AddCleanup(func(_ context.Context) error {
			ran++
			return nil
		})

		for range 2 {
			if err := mod.Init(t.Context()); err != nil {
				t.Fatalf("Init() = %v, want nil", err)
			}
			if err := mod.Destroy(t.Context()); err != nil {
				t.Fatalf("Destroy() = %v, want nil", err)
			}
		}
		if ran != 1 {
			t.Errorf("cleanup ran %d time(s) across two lifecycles, want 1", ran)
		}
	})

	t.Run("ErrorsAndPanicsAreReported", func(t *testing.T) {
		sentinel := errors.New("cleanup boom")
		mod := New(WithConfig(NewConfig("m", "v1")))
		mod.AddCleanup(func(_ context.Context) error { return sentinel })
		mod.AddCleanup(func(_ context.Context) error { panic("nope") })

		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		err := mod.Destroy(t.Context())
		if !errors.Is(err, sentinel) {
			t.Errorf("Destroy() = %v, want to wrap %v", err, sentinel)
		}
		if err == nil || !strings.Contains(err.Error(), "cleanup panicked") {
			t.Errorf("Destroy() = %v, want it to report the panicking cleanup", err)
		}
		if got := mod.State(); got != StateDestroyed {
			t.Errorf("State() = %v, want %v", got, StateDestroyed)
		}
	})

	t.Run("NilIgnored", func(t *testing.T) {
		mod := New(WithConfig(NewConfig("m", "v1")))
		mod.AddCleanup(nil)

		if err := mod.Init(t.Context()); err != nil {
			t.Fatalf("Init() = %v, want nil", err)
		}
		if err := mod.Destroy(t.Context()); err != nil {
			t.Errorf("Destroy() = %v, want nil", err)
		}
	})
}

// TestWithAfterDestroy covers the one functional option with no test of its own,
// and pins the order of the two teardown phases.
func TestWithAfterDestroy(t *testing.T) {
	var order []string
	mod := New(
		WithConfig(NewConfig("m", "v1")),
		WithBeforeDestroy(func(_ context.Context, _ HookModule) error {
			order = append(order, "before")
			return nil
		}),
		WithAfterDestroy(func(_ context.Context, m HookModule) error {
			order = append(order, "after:"+m.State().String())
			return nil
		}),
	)

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}
	if err := mod.Destroy(t.Context()); err != nil {
		t.Fatalf("Destroy() = %v, want nil", err)
	}

	want := []string{"before", "after:Destroyed"}
	if !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

// TestDestroyAfterDestroyFailure covers the second teardown phase failing: the
// module is already marked destroyed by then, so the error is reported without
// resurrecting it.
func TestDestroyAfterDestroyFailure(t *testing.T) {
	boom := errors.New("after destroy boom")
	mod := New(
		WithConfig(NewConfig("m", "v1")),
		WithAfterDestroy(func(_ context.Context, _ HookModule) error { return boom }),
	)

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}

	err := mod.Destroy(t.Context())
	if !errors.Is(err, boom) {
		t.Errorf("Destroy() = %v, want to wrap %v", err, boom)
	}
	// The module passed the point of no return before AfterDestroy ran, so it
	// stays destroyed rather than reverting to Running.
	if got := mod.State(); got != StateDestroyed {
		t.Errorf("State() = %v, want %v", got, StateDestroyed)
	}
}

// TestHookWithNilRunIsSkipped covers a Hook registered without a Run function:
// it is skipped rather than dereferenced.
func TestHookWithNilRunIsSkipped(t *testing.T) {
	var ran []string
	mod := &BaseAppModule{}
	mod.SetConfig(NewConfig("m", "v1"))

	mod.AddHook(PhaseBeforeStart, Hook{Name: "placeholder", Priority: -1})
	mod.AddHook(PhaseBeforeStart, Hook{Name: "real", Run: func(_ context.Context, _ HookModule) error {
		ran = append(ran, "real")
		return nil
	}})

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}
	if !slices.Equal(ran, []string{"real"}) {
		t.Errorf("ran = %v, want [real]", ran)
	}
}
