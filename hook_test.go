package appmod

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func TestPhaseString(t *testing.T) {
	cases := map[Phase]string{
		PhaseBeforeStart:   "BeforeStart",
		PhaseAfterStart:    "AfterStart",
		PhaseBeforeDestroy: "BeforeDestroy",
		PhaseAfterDestroy:  "AfterDestroy",
		Phase(42):          "Phase(42)",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("Phase(%d).String() = %q, want %q", int32(p), got, want)
		}
	}
}

func TestHookPriority(t *testing.T) {
	mod := &BaseAppModule{}

	var order []string
	mod.AddHook(PhaseBeforeStart, Hook{Name: "c", Priority: 10, Run: func(_ context.Context, _ HookModule) error {
		order = append(order, "c")
		return nil
	}})
	mod.AddHook(PhaseBeforeStart, Hook{Name: "a", Priority: -5, Run: func(_ context.Context, _ HookModule) error {
		order = append(order, "a")
		return nil
	}})
	// Equal priority preserves registration order.
	mod.AddHook(PhaseBeforeStart, Hook{Name: "b1", Priority: 0, Run: func(_ context.Context, _ HookModule) error {
		order = append(order, "b1")
		return nil
	}})
	mod.AddHook(PhaseBeforeStart, Hook{Name: "b2", Priority: 0, Run: func(_ context.Context, _ HookModule) error {
		order = append(order, "b2")
		return nil
	}})

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}

	want := []string{"a", "b1", "b2", "c"}
	if !slices.Equal(order, want) {
		t.Errorf("hook order = %v, want %v", order, want)
	}
}

func TestRemoveHook(t *testing.T) {
	mod := &BaseAppModule{}

	var called bool
	mod.AddHook(PhaseBeforeStart, Hook{Name: "tmp", Run: func(_ context.Context, _ HookModule) error {
		called = true
		return nil
	}})

	if removed := mod.RemoveHook(PhaseBeforeStart, "tmp"); !removed {
		t.Error("RemoveHook() = false, want true")
	}
	if removed := mod.RemoveHook(PhaseBeforeStart, "tmp"); removed {
		t.Error("RemoveHook() of an absent hook = true, want false")
	}
	if removed := mod.RemoveHook(PhaseBeforeStart, ""); removed {
		t.Error("RemoveHook(\"\") = true, want false")
	}

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}
	if called {
		t.Error("removed hook was executed")
	}
}

func TestHookErrorType(t *testing.T) {
	mod := &BaseAppModule{}
	mod.SetConfig(NewConfig("mymod", "v1"))

	sentinel := errors.New("boom")
	mod.AddHook(PhaseBeforeStart, Hook{Name: "validate", Run: func(_ context.Context, _ HookModule) error {
		return sentinel
	}})

	err := mod.Init(t.Context())

	var he *HookError
	if !errors.As(err, &he) {
		t.Fatalf("Init() error = %v, want a *HookError", err)
	}
	if he.Phase != PhaseBeforeStart {
		t.Errorf("HookError.Phase = %v, want %v", he.Phase, PhaseBeforeStart)
	}
	if he.Name != "validate" {
		t.Errorf("HookError.Name = %q, want %q", he.Name, "validate")
	}
	if he.Module != "mymod" {
		t.Errorf("HookError.Module = %q, want %q", he.Module, "mymod")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Init() error does not wrap the sentinel: %v", err)
	}
	if !strings.Contains(he.Error(), `module "mymod"`) || !strings.Contains(he.Error(), `"validate"`) {
		t.Errorf("HookError.Error() = %q, missing module/hook name", he.Error())
	}
}

func TestModuleLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mod := New(
		WithConfig(NewConfig("logged", "v1")),
		WithModuleLogger(logger),
	)

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}
	if err := mod.Destroy(t.Context()); err != nil {
		t.Fatalf("Destroy() = %v, want nil", err)
	}

	out := buf.String()
	if !strings.Contains(out, "module initialized") || !strings.Contains(out, "module destroyed") {
		t.Errorf("log output missing lifecycle messages: %q", out)
	}
	if !strings.Contains(out, "module=logged") {
		t.Errorf("log output missing module name: %q", out)
	}
}
