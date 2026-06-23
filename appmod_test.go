package appmod

import (
	"context"
	"errors"
	"sync"
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
		if !mod.Initialized() {
			t.Error("Initialized() = false, want true after Init")
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
		if mod.Initialized() {
			t.Error("Initialized() = true, want false after Destroy")
		}
	})

	t.Run("Events/NormalFly", func(t *testing.T) {
		mod := &BaseAppModule{config: Config{name: `test app`, version: `v1`}}

		var order []string

		newConfig := Config{name: `New Application`, version: `v3`}
		mod.BeforeStart(func(_ context.Context, m AppModule) error {
			order = append(order, "beforeStart")
			m.SetConfig(newConfig)
			return nil
		})
		mod.AfterStart(func(_ context.Context, _ AppModule) error {
			order = append(order, "afterStart")
			return nil
		})

		finishConfig := Config{name: `New Application 2`, version: `v4`}
		mod.BeforeDestroy(func(_ context.Context, m AppModule) error {
			order = append(order, "beforeDestroy")
			m.SetConfig(finishConfig)
			return nil
		})
		mod.AfterDestroy(func(_ context.Context, _ AppModule) error {
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
			mod.BeforeStart(func(_ context.Context, _ AppModule) error {
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

		mod.BeforeStart(func(_ context.Context, _ AppModule) error {
			return error1
		})
		mod.BeforeDestroy(func(_ context.Context, _ AppModule) error {
			return error2
		})

		if err := mod.Init(t.Context()); !errors.Is(err, error1) {
			t.Errorf("Init() = %v, want %v", err, error1)
		}
		// BeforeStart failed, so the module stays not initialized.
		if mod.Initialized() {
			t.Error("Initialized() = true, want false after failed Init")
		}

		// Force the module into the running state to exercise Destroy hook.
		mod.setState(StateRunning)
		if err := mod.Destroy(t.Context()); !errors.Is(err, error2) {
			t.Errorf("Destroy() = %v, want %v", err, error2)
		}
	})

	t.Run("Events/HookPanic", func(t *testing.T) {
		mod := &BaseAppModule{}
		mod.BeforeStart(func(_ context.Context, _ AppModule) error {
			panic("boom")
		})

		err := mod.Init(t.Context())
		if err == nil {
			t.Fatal("Init() = nil, want error after panicking hook")
		}
		if mod.Initialized() {
			t.Error("Initialized() = true, want false after panicking hook")
		}
	})

	t.Run("New/Options", func(t *testing.T) {
		var started bool
		mod := New(
			WithConfig(NewConfig("opt", "v1")),
			WithBeforeStart(func(_ context.Context, _ AppModule) error {
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
				mod.BeforeStart(func(_ context.Context, _ AppModule) error { return nil })
				_ = mod.Init(t.Context())
				_ = mod.Initialized()
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
	t.Run("BeforeStartFailureRunsTeardownReversed", func(t *testing.T) {
		mod := &BaseAppModule{}

		var order []string
		mod.BeforeStart(func(_ context.Context, _ AppModule) error {
			order = append(order, "beforeStart")
			return errors.New("boom")
		})
		mod.BeforeDestroy(func(_ context.Context, _ AppModule) error {
			order = append(order, "beforeDestroy1")
			return nil
		})
		mod.BeforeDestroy(func(_ context.Context, _ AppModule) error {
			order = append(order, "beforeDestroy2")
			return nil
		})
		mod.AfterDestroy(func(_ context.Context, _ AppModule) error {
			order = append(order, "afterDestroy")
			return nil
		})

		if err := mod.Init(t.Context()); err == nil {
			t.Fatal("Init() = nil, want error")
		}
		if got := mod.State(); got != StateFailed {
			t.Errorf("State() = %v, want %v", got, StateFailed)
		}

		// Teardown runs in reverse registration order: BeforeDestroy hooks
		// (reversed) then AfterDestroy hooks (reversed).
		want := []string{"beforeStart", "beforeDestroy2", "beforeDestroy1", "afterDestroy"}
		if len(order) != len(want) {
			t.Fatalf("order = %v, want %v", order, want)
		}
		for i := range want {
			if order[i] != want[i] {
				t.Fatalf("order = %v, want %v", order, want)
			}
		}
	})

	t.Run("AfterStartFailureRollsBack", func(t *testing.T) {
		mod := &BaseAppModule{}

		var torndown bool
		mod.AfterStart(func(_ context.Context, _ AppModule) error {
			return errors.New("after start boom")
		})
		mod.BeforeDestroy(func(_ context.Context, _ AppModule) error {
			torndown = true
			return nil
		})

		if err := mod.Init(t.Context()); err == nil {
			t.Fatal("Init() = nil, want error")
		}
		if mod.Initialized() {
			t.Error("Initialized() = true, want false after AfterStart failure")
		}
		if got := mod.State(); got != StateFailed {
			t.Errorf("State() = %v, want %v", got, StateFailed)
		}
		if !torndown {
			t.Error("teardown hook was not run during rollback")
		}
	})

	t.Run("RollbackErrorIsJoined", func(t *testing.T) {
		mod := &BaseAppModule{}

		startErr := errors.New("start boom")
		rollbackErr := errors.New("teardown boom")
		mod.BeforeStart(func(_ context.Context, _ AppModule) error {
			return startErr
		})
		mod.BeforeDestroy(func(_ context.Context, _ AppModule) error {
			return rollbackErr
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
		mod.BeforeStart(func(_ context.Context, _ AppModule) error {
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
		mod.BeforeStart(func(_ context.Context, _ AppModule) error {
			cancel()
			return nil
		})
		mod.BeforeStart(func(_ context.Context, _ AppModule) error {
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
