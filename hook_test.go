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

func TestPhaseValid(t *testing.T) {
	for _, p := range []Phase{PhaseBeforeStart, PhaseAfterStart, PhaseBeforeDestroy, PhaseAfterDestroy} {
		if !p.Valid() {
			t.Errorf("Phase(%v).Valid() = false, want true", p)
		}
	}
	for _, p := range []Phase{Phase(-1), Phase(4), Phase(42)} {
		if p.Valid() {
			t.Errorf("Phase(%d).Valid() = true, want false", int32(p))
		}
	}
}

// TestUnknownPhasePanics pins the fix for a silent failure: an out-of-range
// phase used to make AddHook drop the hook without a word, turning start-up
// logic into code that simply never ran.
func TestUnknownPhasePanics(t *testing.T) {
	cases := map[string]func(){
		"AddHook": func() {
			(&BaseAppModule{}).AddHook(Phase(42), Hook{Name: "ghost"})
		},
		"RemoveHook": func() {
			(&BaseAppModule{}).RemoveHook(Phase(42), "ghost")
		},
		"WithHook": func() {
			_ = New(WithHook(Phase(42), Hook{Name: "ghost"}))
		},
	}

	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s(Phase(42)) did not panic", name)
				}
				if msg, ok := r.(string); !ok || !strings.Contains(msg, "invalid phase Phase(42)") {
					t.Errorf("panic = %v, want a message naming the invalid phase", r)
				}
			}()
			fn()
		})
	}
}

// TestValidPhaseDoesNotPanic guards against the check being too eager.
func TestValidPhaseDoesNotPanic(t *testing.T) {
	mod := &BaseAppModule{}
	for _, p := range []Phase{PhaseBeforeStart, PhaseAfterStart, PhaseBeforeDestroy, PhaseAfterDestroy} {
		mod.AddHook(p, Hook{Name: "h", Run: func(_ context.Context, _ HookModule) error { return nil }})
		if !mod.RemoveHook(p, "h") {
			t.Errorf("RemoveHook(%v) = false, want true", p)
		}
	}
}

// TestSetLogger covers the imperative counterpart of WithModuleLogger.
func TestSetLogger(t *testing.T) {
	var buf bytes.Buffer
	mod := &BaseAppModule{}
	mod.SetConfig(NewConfig("imperative", "v1"))
	mod.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if err := mod.Init(t.Context()); err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "module=imperative") {
		t.Errorf("log output missing the module name: %q", buf.String())
	}

	// A nil logger disables logging without panicking.
	mod.SetLogger(nil)
	if err := mod.Destroy(t.Context()); err != nil {
		t.Errorf("Destroy() after SetLogger(nil) = %v, want nil", err)
	}
}

// TestHookErrorWithoutModule covers the branch of HookError.Error that omits the
// module prefix. A module built by this package always has a name, so the branch
// is only reachable for a HookError a caller constructed itself.
func TestHookErrorWithoutModule(t *testing.T) {
	cause := errors.New("boom")

	anonymous := &HookError{Phase: PhaseAfterStart, Index: 2, Err: cause}
	if got, want := anonymous.Error(), "appmod: AfterStart hook #2 failed: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	named := &HookError{Phase: PhaseBeforeDestroy, Index: 0, Name: "close-db", Err: cause}
	if got, want := named.Error(), `appmod: BeforeDestroy hook "close-db" failed: boom`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(named, cause) {
		t.Error("HookError does not unwrap to its cause")
	}
}
