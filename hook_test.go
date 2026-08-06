package appmod

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
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

// TestHookPriorityExtremes guards the ordering against integer overflow: a
// comparator written as (a.Priority - b.Priority) wraps around for operands this
// far apart and silently inverts the order.
func TestHookPriorityExtremes(t *testing.T) {
	mod := &BaseAppModule{}

	var order []string
	add := func(name string, prio int) {
		mod.AddHook(PhaseBeforeStart, Hook{Name: name, Priority: prio, Run: func(_ context.Context, _ HookModule) error {
			order = append(order, name)
			return nil
		}})
	}
	add("max", math.MaxInt)
	add("min", math.MinInt)
	add("zero", 0)

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}

	want := []string{"min", "zero", "max"}
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

// TestHookModuleIsOpaque pins the narrowing of the hook view. The hook used to
// receive *BaseAppModule itself, so one type assertion recovered the full API:
// a hook could re-enter Init/Destroy or mutate the hook set while running.
func TestHookModuleIsOpaque(t *testing.T) {
	mod := &BaseAppModule{}
	mod.SetConfig(NewConfig("m", "v1"))

	var (
		asLifecycle    bool
		asHookRegistry bool
		asConcrete     bool
		name           string
		state          State
	)
	mod.AfterStart(func(_ context.Context, m HookModule) error {
		_, asLifecycle = m.(Lifecycle)
		_, asHookRegistry = m.(HookRegistry)
		_, asConcrete = m.(*BaseAppModule)
		name, state = m.Name(), m.State()

		return nil
	})

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}

	if asLifecycle {
		t.Error("HookModule asserts to Lifecycle: a hook can re-enter Init/Destroy")
	}
	if asHookRegistry {
		t.Error("HookModule asserts to HookRegistry: a hook can mutate the hook set while running")
	}
	if asConcrete {
		t.Error("HookModule asserts to *BaseAppModule: the whole module escapes the view")
	}

	// The view must still expose everything HookModule promises.
	if name != "m" {
		t.Errorf("Name() = %q, want %q", name, "m")
	}
	if state != StateInitializing {
		t.Errorf("State() = %v, want %v", state, StateInitializing)
	}
}
