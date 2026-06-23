package appmod

import (
	"context"
	"errors"
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

	t.Run("Events/PanicMode", func(t *testing.T) {
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

		// Force the module into the initialized state to exercise Destroy hook.
		mod.initialized = true
		if err := mod.Destroy(t.Context()); !errors.Is(err, error2) {
			t.Errorf("Destroy() = %v, want %v", err, error2)
		}
	})
}
